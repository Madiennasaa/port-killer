package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type PortItem struct {
	Proto   string
	Addr    string
	Port    string
	Process string
	PID     string
	Uptime  string
	Source  string
	Cwd     string
	Project string
	Health  HealthStatus
	Latency time.Duration
	Orphan  bool
}

func (p PortItem) IsDocker() bool { return p.Source == "docker" }

func GetActivePorts() []PortItem {
	var items []PortItem

	cmd := exec.Command("ss", "-tulnp")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil {
		items = append(items, parseSS(out.String())...)
	}

	items = append(items, GetDockerPorts()...)
	fillUptime(items)
	enrichAll(items)
	return items
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

// ssItem converts one `ss` line into a PortItem. ok is false when the line
// carries no process info or names docker-proxy (the docker source owns those).
func ssItem(fields []string) (PortItem, bool) {
	addr, port, ok := splitAddr(fields[4])
	if !ok {
		return PortItem{}, false
	}
	pid, procName := parseProcessInfo(fields[len(fields)-1])
	if pid == "" || procName == "docker-proxy" {
		return PortItem{}, false
	}
	return PortItem{
		Proto:   fields[0],
		Addr:    addr,
		Port:    port,
		Process: procName,
		PID:     pid,
		Source:  "host",
	}, true
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
		it, ok := ssItem(fields)
		if !ok {
			continue
		}
		key := it.Proto + "-" + it.Port + "-" + it.PID
		if seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, it)
	}
	return items
}

// enrichAll fills project/cwd and the orphan flag, caching per pid.
func enrichAll(items []PortItem) {
	cache := make(map[string][2]string) // pid -> {cwd, project}
	orphanCache := map[string]bool{}
	for i := range items {
		if items[i].IsDocker() {
			enrichProject(&items[i])
			items[i].Orphan = false
			continue
		}
		if v, ok := cache[items[i].PID]; ok {
			items[i].Cwd, items[i].Project = v[0], v[1]
		} else {
			enrichProject(&items[i])
			cache[items[i].PID] = [2]string{items[i].Cwd, items[i].Project}
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
	out, err := exec.Command("ps", "-o", "pid=,etime=", "-p", strings.Join(pids, ",")).Output()
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
		if !strings.HasPrefix(it.Process, "docker:") {
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

func GetDockerPorts() []PortItem {
	out, err := exec.Command("docker", "ps", "--format", "{{.ID}}|{{.Names}}|{{.Ports}}|{{.Status}}").Output()
	if err != nil {
		return nil
	}
	return parseDockerPS(string(out))
}

// parseDockerPS turns `docker ps --format` output into published-port items.
// Lines without "->" (no publish) and malformed lines are skipped.
func parseDockerPS(out string) []PortItem {
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
				Process: "docker:" + name,
				PID:     id,
				Uptime:  uptime,
				Source:  "docker",
			})
		}
	}
	return items
}

func parseProcessInfo(info string) (string, string) {
	if !strings.Contains(info, "pid=") {
		return "", "unknown"
	}

	procName := "unknown"
	if idx := strings.Index(info, `"`); idx != -1 {
		rest := info[idx+1:]
		if endIdx := strings.Index(rest, `"`); endIdx != -1 {
			procName = rest[:endIdx]
		}
	}

	pid := ""
	if idx := strings.Index(info, "pid="); idx != -1 {
		rest := info[idx+4:]
		for _, ch := range rest {
			if ch >= '0' && ch <= '9' {
				pid += string(ch)
			} else {
				break
			}
		}
	}
	return pid, procName
}

func KillProcess(pid string, force bool) error {
	n, err := strconv.Atoi(pid)
	if err != nil {
		return err
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(n, sig)
}

func StopContainer(id string) error {
	out, err := exec.Command("docker", "stop", id).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
