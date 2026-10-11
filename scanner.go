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
		return []PortItem{}
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

// Cari container Docker yang mempublikasikan port host tertentu.
func FindContainerByPort(port string) (string, error) {
	out, err := exec.Command("docker", "ps", "--filter", "publish="+port, "--format", "{{.ID}}").Output()
	if err != nil {
		return "", err
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return "", fmt.Errorf("container tidak ditemukan untuk port %s", port)
	}
	return ids[0], nil
}

func StopContainer(id string) error {
	return exec.Command("docker", "stop", id).Run()
}
