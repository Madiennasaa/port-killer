package main

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestSupervisorFromEnv(t *testing.T) {
	cases := []struct {
		env  []string
		hint string
	}{
		{[]string{"PATH=/bin", "pm_id=3", "name=api"}, "pm2 stop 3"},
		{[]string{"SUPERVISOR_PROCESS_NAME=web", "SUPERVISOR_GROUP_NAME=web"}, "supervisorctl stop web"},
		{[]string{"SUPERVISOR_PROCESS_NAME=web_0", "SUPERVISOR_GROUP_NAME=web"}, "supervisorctl stop web:web_0"},
		{[]string{"SUPERVISOR_PROCESS_NAME=solo"}, "supervisorctl stop solo"},
	}
	for _, c := range cases {
		if s, ok := supervisorFromEnv(c.env); !ok || s.Hint != c.hint {
			t.Errorf("%q: %+v %v", c.env, s, ok)
		}
	}
	if _, ok := supervisorFromEnv([]string{"PATH=/bin", "garbage"}); ok {
		t.Error("plain env must not match")
	}
}

func TestSupervisorFromAncestor(t *testing.T) {
	cases := []struct {
		comm string
		argv []string
		want string
	}{
		{"PM2 v5.3.0: God", nil, "pm2"},
		{"node", []string{"PM2 v5.3.0: God Daemon (/root/.pm2)"}, "pm2"},
		{"runsv", nil, "runit"},
		{"s6-supervise", nil, "s6"},
		{"supervisord", nil, "supervisord"},
		{"python3", []string{"/usr/bin/python3", "/usr/bin/supervisord", "-c", "x"}, "supervisord"},
		{"node", []string{"node", "/usr/lib/node_modules/nodemon/bin/nodemon"}, "nodemon"},
		{"node", []string{"node", "/usr/bin/forever", "start"}, "forever"},
		{"bash", []string{"bash", "-c", "x", "nodemon"}, ""}, // only the first three argv entries count
	}
	for _, c := range cases {
		s, ok := supervisorFromAncestor(c.comm, c.argv)
		if s.Name != c.want || ok != (c.want != "") {
			t.Errorf("%q %q = %+v %v, want %q", c.comm, c.argv, s, ok, c.want)
		}
	}
}

// stubProcTree serves procFile from files (path -> content); anything else
// is missing.
func stubProcTree(t *testing.T, files map[string]string) {
	t.Helper()
	old := procFile
	t.Cleanup(func() { procFile = old })
	procFile = func(p string) ([]byte, error) {
		if s, ok := files[p]; ok {
			return []byte(s), nil
		}
		return nil, os.ErrNotExist
	}
}

func fakeStat(pid, ppid int) string {
	return strconv.Itoa(pid) + " (x) S " + strconv.Itoa(ppid) + " 1 1 0 -1 0"
}

func TestSupervisorOf(t *testing.T) {
	stubProcTree(t, map[string]string{
		"/proc/500/stat":    fakeStat(500, 450),
		"/proc/450/stat":    fakeStat(450, 400),
		"/proc/450/comm":    "sh\n",
		"/proc/400/comm":    "node\n",
		"/proc/400/cmdline": "node\x00/usr/bin/nodemon\x00app.js\x00",
		"/proc/600/environ": "pm_id=7\x00",
		"/proc/700/stat":    fakeStat(700, 1),
		"/proc/800/stat":    "garbage",
	})
	if s, ok := supervisorOf(500); !ok || s.Name != "nodemon" {
		t.Errorf("ancestor walk: %+v %v", s, ok)
	}
	if s, ok := supervisorOf(600); !ok || s.Hint != "pm2 stop 7" {
		t.Errorf("env tag: %+v %v", s, ok)
	}
	for _, pid := range []int{700, 800, 900} { // reaches init, bad stat, gone
		if s, ok := supervisorOf(pid); ok {
			t.Errorf("pid %d: %+v", pid, s)
		}
	}
}

func TestSupervisorOfDepthLimit(t *testing.T) {
	old := procFile
	t.Cleanup(func() { procFile = old })
	reads := 0
	procFile = func(p string) ([]byte, error) {
		if !strings.HasSuffix(p, "/stat") {
			return nil, errors.New("no")
		}
		reads++
		pid, _ := strconv.Atoi(strings.Split(p, "/")[2])
		return []byte(fakeStat(pid, pid+1)), nil // an endless chain of shells
	}
	if _, ok := supervisorOf(100); ok || reads != maxSupervisorDepth {
		t.Errorf("ok %v reads %d", ok, reads)
	}
}

func TestWarnSupervised(t *testing.T) {
	stubProcTree(t, map[string]string{"/proc/600/environ": "pm_id=7\x00"})
	out := captureStderr(t, func() {
		warnSupervised([]PortItem{
			{PID: "600", Process: "api"},
			{PID: "600", Process: "api", Proto: "tcp6"},
			{PID: "abc", Process: "docker:web", Source: "docker"},
			{PID: "bad", Process: "x"},
			{PID: "601", Process: "plain"},
		})
	})
	if strings.Count(out, "warning:") != 1 || !strings.Contains(out, "api (PID 600) is managed by pm2") || !strings.Contains(out, "pm2 stop 7") {
		t.Errorf("warnings: %q", out)
	}
}

func TestBlastTextSupervisor(t *testing.T) {
	sup := Supervisor{"pm2", "pm2 stop 7"}
	if got := blastText(Blast{Super: sup}, false); got != "no impact detected; pm2 may restart it (pm2 stop 7)" {
		t.Errorf("no impact: %q", got)
	}
	if got := blastText(Blast{Conns: 1, Super: sup}, false); !strings.HasPrefix(got, "dropping 1 connections, 0 child processes; pm2") {
		t.Errorf("with conns: %q", got)
	}
}

func TestCLIDryRunWarnsSupervised(t *testing.T) {
	stubProcTree(t, map[string]string{"/proc/42/environ": "SUPERVISOR_PROCESS_NAME=web\x00"})
	stubActivePorts(t, []PortItem{{Port: "3000", PID: "42", Process: "gunicorn"}})
	stubExit(t)
	var out string
	errOut := captureStderr(t, func() { out = captureStdout(t, func() { cliKill([]string{"--kill", "3000", "--dry-run"}, false) }) })
	if !strings.Contains(out, "would stop gunicorn") || !strings.Contains(errOut, "supervisorctl stop web") {
		t.Errorf("out %q err %q", out, errOut)
	}
}
