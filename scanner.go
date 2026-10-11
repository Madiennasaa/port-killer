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
	Port    string
	Process string
	PID     string
}

func GetActivePorts() []PortItem {
	cmd := exec.Command("ss", "-tulnp")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return GetDockerPorts()
	}

	var items []PortItem
	seen := make(map[string]bool)

	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.Contains(line, "LISTEN") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}

		localAddr := fields[4]
		parts := strings.Split(localAddr, ":")
		port := parts[len(parts)-1]

		pid, procName := parseProcessInfo(fields[len(fields)-1])
		if pid == "" {
			continue
		}

		// Port Docker ditampilkan lewat GetDockerPorts()
		if procName == "docker-proxy" {
			continue
		}

		key := port + "-" + pid
		if seen[key] {
			continue
		}
		seen[key] = true

		items = append(items, PortItem{
			Port:    port,
			Process: procName,
			PID:     pid,
		})
	}
	return append(items, GetDockerPorts()...)
}

func GetDockerPorts() []PortItem {
	out, err := exec.Command("docker", "ps", "--format", "{{.ID}}|{{.Names}}|{{.Ports}}").Output()
	if err != nil {
		return nil
	}

	var items []PortItem
	seen := make(map[string]bool)

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		id, name, ports := parts[0], parts[1], parts[2]

		for _, p := range strings.Split(ports, ",") {
			p = strings.TrimSpace(p)
			if !strings.Contains(p, "->") {
				continue
			}
			hostPart := strings.Split(p, "->")[0]
			hostParts := strings.Split(hostPart, ":")
			port := hostParts[len(hostParts)-1]

			key := port + "-" + id
			if seen[key] {
				continue
			}
			seen[key] = true

			items = append(items, PortItem{
				Port:    port,
				Process: "docker:" + name,
				PID:     id,
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
