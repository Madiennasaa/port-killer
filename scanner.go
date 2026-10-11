package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

type PortItem struct {
	Proto   string
	Addr    string
	Port    string
	Process string
	PID     string
	Uptime  string
}

func GetActivePorts() []PortItem {
	var items []PortItem

	cmd := exec.Command("ss", "-tulnp")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return GetDockerPorts()
	}

	seen := make(map[string]bool)
	for _, line := range strings.Split(out.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		state := fields[1]
		if state != "LISTEN" && state != "UNCONN" {
			continue
		}

		proto := fields[0]
		localAddr := fields[4]
		i := strings.LastIndex(localAddr, ":")
		if i == -1 {
			continue
		}
		addr, port := localAddr[:i], localAddr[i+1:]

		pid, procName := parseProcessInfo(fields[len(fields)-1])
		if pid == "" || procName == "docker-proxy" {
			continue
		}

		key := proto + "-" + port + "-" + pid
		if seen[key] {
			continue
		}
		seen[key] = true

		items = append(items, PortItem{
			Proto:   proto,
			Addr:    addr,
			Port:    port,
			Process: procName,
			PID:     pid,
		})
	}

	items = append(items, GetDockerPorts()...)
	fillUptime(items)
	return items
}

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
	out, _ := exec.Command("ps", "-o", "pid=,etime=", "-p", strings.Join(pids, ",")).Output()
	m := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) == 2 {
			m[f[0]] = f[1]
		}
	}
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

	var items []PortItem
	seen := make(map[string]bool)

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
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
