package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type PortItem struct {
	Proto     string
	Addr      string
	Port      string
	Process   string
	PID       string
	Uptime    string
	Source    string
	Cwd       string
	Project   string
	Branch    string // git branch (or short commit) of the project, "" outside a repo
	Framework string // dev server or framework behind the command line, "" if unknown
	Health    HealthStatus
	Latency   time.Duration
	Orphan    bool
	Cpu       string
	Mem       string

	// ctrUser is the account a rootless podman container belongs to ("" =
	// the user portkiller runs as), so stop/start run under that account.
	ctrUser string

	// startTime is the kernel jiffie timestamp of the owning process, used to
	// refuse a kill when the PID has been reused. Unexported: it is an
	// implementation detail, not part of the --json contract.
	startTime uint64
}

// IsContainer reports whether the item is a published container port (docker
// or podman) rather than a host process.
func (p PortItem) IsContainer() bool { return p.Source == "docker" || p.Source == "podman" }

// scanPorts collects listeners. The error is non-nil when ss fails, so the TUI
// can show it instead of silently rendering an empty table; docker results are
// still returned in that case.
func scanPorts() ([]PortItem, error) {
	// Non-nil from the start: --list --json must print "[]" (not "null") when
	// nothing is listening, and callers treat the result as always present.
	items := []PortItem{}

	var scanErr error
	if out, err := commandOutput(ssBin, "-tulnp"); err == nil {
		items = append(items, parseSS(string(out))...)
	} else {
		scanErr = fmt.Errorf("ss -tulnp: %w", err)
	}

	items = append(items, containerPorts()...)
	items = dropContainerProxies(items)
	fillUptime(items)
	enrichAll(items)
	enrichUsages(items)
	return items, scanErr
}

// ssListening reports whether an `ss` line is a listening socket we show.
func ssListening(fields []string) bool {
	if len(fields) < 7 {
		return false
	}
	switch fields[1] {
	case "LISTEN", "UNCONN":
		return true
	}
	return false
}

// splitAddr splits "host:port" into its two halves; ok is false when there is
// no ":" to split on (malformed or header lines).
func splitAddr(localAddr string) (addr, port string, ok bool) {
	i := strings.LastIndex(localAddr, ":")
	if i == -1 {
		return "", "", false
	}
	return localAddr[:i], localAddr[i+1:], true
}

// ssItems converts one `ss` line into a PortItem per owning process, since a
// socket may be shared by several pids (SO_REUSEPORT). Lines without process
// info and docker-proxy listeners (owned by the docker source) are dropped.
func ssItems(fields []string) []PortItem {
	addr, port, ok := splitAddr(fields[4])
	if !ok {
		return nil
	}
	refs := parseProcessInfos(fields[len(fields)-1])
	var out []PortItem
	for _, r := range refs {
		if r[1] == "docker-proxy" {
			continue
		}
		out = append(out, PortItem{
			Proto:   fields[0],
			Addr:    addr,
			Port:    port,
			Process: r[1],
			PID:     r[0],
			Source:  "host",
		})
	}
	return out
}

// parseSS turns `ss -tulnp` output into host listeners.
// Header/short/state-filtered lines and docker-proxy listeners are dropped,
// and a pid+port seen twice is kept only once.
func parseSS(out string) []PortItem {
	var items []PortItem
	seen := make(map[string]bool)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if !ssListening(fields) {
			continue
		}
		for _, it := range ssItems(fields) {
			key := it.Proto + "-" + it.Port + "-" + it.PID
			if seen[key] {
				continue
			}
			seen[key] = true
			items = append(items, it)
		}
	}
	return items
}

// enrichAll fills project/cwd and the orphan flag, caching per pid.
func enrichAll(items []PortItem) {
	cache := make(map[string][4]string) // pid -> {cwd, project, branch, framework}
	orphanCache := map[string]bool{}
	for i := range items {
		if items[i].IsContainer() {
			enrichProject(&items[i])
			items[i].Orphan = false
			continue
		}
		if v, ok := cache[items[i].PID]; ok {
			items[i].Cwd, items[i].Project, items[i].Branch, items[i].Framework = v[0], v[1], v[2], v[3]
		} else {
			enrichProject(&items[i])
			cache[items[i].PID] = [4]string{items[i].Cwd, items[i].Project, items[i].Branch, items[i].Framework}
		}
		if v, ok := orphanCache[items[i].PID+"|"+items[i].Proto]; ok {
			items[i].Orphan = v
			continue
		}
		enrichOrphan(&items[i])
		orphanCache[items[i].PID+"|"+items[i].Proto] = items[i].Orphan
	}
}

// psUptimeMap asks ps for the elapsed time of the given pids. A failed ps run
// (missing binary, too many pids) yields an empty map rather than an error:
// uptime is decoration, not data worth failing a scan for.
func psUptimeMap(pids []string) map[string]string {
	out, err := commandOutput(psBin, "-o", "pid=,etime=", "-p", strings.Join(pids, ","))
	if err != nil && len(out) == 0 {
		return map[string]string{}
	}
	m := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) == 2 {
			m[f[0]] = f[1]
		}
	}
	return m
}

// fillUptime stamps each non-docker item with its process elapsed time.
func fillUptime(items []PortItem) {
	var pids []string
	for _, it := range items {
		if !it.IsContainer() {
			pids = append(pids, it.PID)
		}
	}
	if len(pids) == 0 {
		return
	}
	m := psUptimeMap(pids)
	for i := range items {
		if u, ok := m[items[i].PID]; ok {
			items[i].Uptime = u
		}
	}
}

// parseContainerPS turns `docker ps` / `podman ps --format` output into
// published-port items tagged with runtime ("docker" or "podman"). Lines
// without "->" (no publish) and malformed lines are skipped.
func parseContainerPS(out, runtime string) []PortItem {
	var items []PortItem
	seen := make(map[string]bool)

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}
		id, name, ports := parts[0], parts[1], parts[2]
		uptime := strings.TrimPrefix(parts[3], "Up ")

		for _, p := range strings.Split(ports, ",") {
			p = strings.TrimSpace(p)
			if !strings.Contains(p, "->") {
				continue
			}
			segs := strings.Split(p, "->")
			hostPart := segs[0]
			proto := "tcp"
			if j := strings.LastIndex(segs[1], "/"); j != -1 {
				proto = segs[1][j+1:]
			}
			k := strings.LastIndex(hostPart, ":")
			if k == -1 {
				continue
			}
			addr, port := hostPart[:k], hostPart[k+1:]

			key := proto + "-" + port + "-" + id
			if seen[key] {
				continue
			}
			seen[key] = true

			items = append(items, PortItem{
				Proto:   proto,
				Addr:    addr,
				Port:    port,
				Process: runtime + ":" + name,
				PID:     id,
				Uptime:  uptime,
				Source:  runtime,
			})
		}
	}
	return items
}

// parseProcessInfos extracts every (pid, name) pair from ss users:(...) data.
func parseProcessInfos(info string) [][2]string {
	var out [][2]string
	for {
		ref, rest, ok := nextProcRef(info)
		if !ok {
			return out
		}
		out = append(out, ref)
		info = rest
	}
}

// nextProcRef parses the first name/pid pair in info and returns the remainder.
func nextProcRef(info string) ([2]string, string, bool) {
	i := strings.Index(info, `("`)
	if i < 0 {
		return [2]string{}, "", false
	}
	info = info[i+2:]
	j := strings.Index(info, `"`)
	if j < 0 {
		return [2]string{}, "", false
	}
	name := info[:j]
	rest := info[j+1:]
	p := strings.Index(rest, "pid=")
	if p < 0 {
		return [2]string{}, "", false
	}
	digits := rest[p+4:]
	n := 0
	for n < len(digits) && digits[n] >= '0' && digits[n] <= '9' {
		n++
	}
	if n == 0 {
		return [2]string{}, "", false
	}
	return [2]string{digits[:n], name}, digits[n:], true
}

func parseProcessInfo(info string) (string, string) {
	refs := parseProcessInfos(info)
	if len(refs) == 0 {
		return "", "unknown"
	}
	return refs[0][0], refs[0][1]
}

// KillProcess stops a process by pid with the same graceful ladder as the TUI.
func KillProcess(pid string, force bool) error {
	n, err := strconv.Atoi(pid)
	if err != nil {
		return err
	}
	return stopPID(n, force)
}
