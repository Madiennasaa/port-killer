package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// maxSupervisorDepth bounds the ancestor walk; supervisors sit a few levels up
// at most (pm2 -> node -> app, nodemon -> sh -> app).
const maxSupervisorDepth = 6

// Supervisor is a process manager that may start a stopped listener again,
// with the command that stops it for good.
type Supervisor struct {
	Name string
	Hint string
}

// supervisorFromEnv (pure) recognises the tags pm2 and supervisord put in
// their children's environment, which also carry the app's id or name.
func supervisorFromEnv(env []string) (Supervisor, bool) {
	vals := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vals[k] = v
		}
	}
	if id, ok := vals["pm_id"]; ok {
		return Supervisor{"pm2", "pm2 stop " + id}, true
	}
	if name, ok := vals["SUPERVISOR_PROCESS_NAME"]; ok {
		if g := vals["SUPERVISOR_GROUP_NAME"]; g != "" && g != name {
			name = g + ":" + name
		}
		return Supervisor{"supervisord", "supervisorctl stop " + name}, true
	}
	return Supervisor{}, false
}

// supervisorFromAncestor (pure) recognises a supervisor from an ancestor's
// comm and argv. Script-based ones (nodemon, forever) run under an
// interpreter, so the first few argv entries are checked by base name.
func supervisorFromAncestor(comm string, argv []string) (Supervisor, bool) {
	if strings.HasPrefix(comm, "PM2 v") || (len(argv) > 0 && strings.HasPrefix(argv[0], "PM2 v")) {
		return Supervisor{"pm2", "pm2 stop <app>"}, true
	}
	switch comm {
	case "runsv":
		return Supervisor{"runit", "sv down <service>"}, true
	case "s6-supervise":
		return Supervisor{"s6", "s6-svc -d <service>"}, true
	}
	names := []string{comm}
	for i := 0; i < len(argv) && i < 3; i++ {
		names = append(names, filepath.Base(argv[i]))
	}
	for _, n := range names {
		switch n {
		case "supervisord":
			return Supervisor{"supervisord", "supervisorctl stop <program>"}, true
		case "nodemon":
			return Supervisor{"nodemon", "quit nodemon itself (Ctrl+C in its terminal)"}, true
		case "forever":
			return Supervisor{"forever", "forever stop <app>"}, true
		}
	}
	return Supervisor{}, false
}

// supervisorOf finds the process manager behind pid, if any: first from its
// own environment, then by walking its ancestors up to (not including) init.
func supervisorOf(pid int) (Supervisor, bool) {
	if s, ok := supervisorFromEnv(capturedEnv(strconv.Itoa(pid))); ok {
		return s, true
	}
	p := pid
	for i := 0; i < maxSupervisorDepth; i++ {
		b, err := procFile("/proc/" + strconv.Itoa(p) + "/stat")
		if err != nil {
			return Supervisor{}, false
		}
		ppid, _, err := parseStat(b)
		if err != nil || ppid <= 1 {
			return Supervisor{}, false
		}
		dir := "/proc/" + strconv.Itoa(ppid)
		comm, _ := procFile(dir + "/comm")
		argv, _ := procFile(dir + "/cmdline")
		if s, ok := supervisorFromAncestor(strings.TrimSpace(string(comm)), parseCmdline(argv)); ok {
			return s, true
		}
		p = ppid
	}
	return Supervisor{}, false
}

// warnSupervised tells a CLI user, once per PID, which listeners a process
// manager may bring straight back, and how to stop them there instead.
func warnSupervised(items []PortItem) {
	seen := map[string]bool{}
	for _, it := range items {
		if it.IsContainer() || seen[it.PID] {
			continue
		}
		seen[it.PID] = true
		pid, err := strconv.Atoi(it.PID)
		if err != nil {
			continue
		}
		if s, ok := supervisorOf(pid); ok {
			fmt.Fprintf(os.Stderr, "warning: %s (PID %s) is managed by %s and may come back; to stop it for good: %s\n", it.Process, it.PID, s.Name, s.Hint)
		}
	}
}
