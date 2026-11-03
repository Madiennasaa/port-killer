package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDetectFramework(t *testing.T) {
	for want, argv := range map[string][]string{
		"Vite":                {"node", "/app/node_modules/.bin/vite", "--port", "5173"},
		"Next.js":             {"next-server (v14.2.3)"},
		"go run":              {"/tmp/go-build123/b001/exe/main"},
		"Django":              {"python3", "manage.py", "runserver", "0.0.0.0:8000"},
		"Rails":               {"ruby", "bin/rails", "s"},
		"Uvicorn":             {"python", "-m", "uvicorn", "app:app"},
		"Storybook":           {"node", "/x/storybook/bin/storybook.mjs", "dev"},
		"PHP built-in server": {"php", "-S", "localhost:8000"},
		"":                    {"php", "artisan", "migrate"}, // artisan without serve
	} {
		if got := detectFramework(argv); got != want {
			t.Errorf("detectFramework(%q) = %q, want %q", argv, got, want)
		}
	}
	for _, argv := range [][]string{nil, {"node", "server.js"}, {"node", "a", "b", "c", "vite"}} {
		if got := detectFramework(argv); got != "" {
			t.Errorf("detectFramework(%q) = %q, want none", argv, got)
		}
	}
}

func TestProcFramework(t *testing.T) {
	stubProcTree(t, map[string]string{"/proc/10/cmdline": "node\x00vite.js\x00"})
	if got := procFramework("10"); got != "Vite" {
		t.Errorf("procFramework = %q", got)
	}
	if got := procFramework("11"); got != "" {
		t.Errorf("unreadable cmdline: %q", got)
	}
	if got := detailLines(PortItem{Process: "node", Framework: "Vite"}, Detail{})[0]; !strings.HasSuffix(got, "[Vite]") {
		t.Errorf("detail header: %q", got)
	}
}

func TestCaptureEachMergesPorts(t *testing.T) {
	got := captureEach([]PortItem{
		{Source: "docker", PID: "c1", Port: "80", Process: "docker:web"},
		{Source: "docker", PID: "c1", Port: "443", Process: "docker:web"},
		{Source: "docker", PID: "c1", Port: "80", Process: "docker:web"},
		{PID: "bogus", Port: "1"},
		{PID: "bogus", Port: "2"},
	})
	if len(got) != 2 || strings.Join(got[0].r.Ports, ",") != "80,443" || got[1].ok {
		t.Errorf("captureEach: %+v", got)
	}
}

func TestRestartWhenFree(t *testing.T) {
	stubScans(t, []PortItem{{Port: "3000"}})
	host := Restartable{Name: "vite", Cmd: []string{"/nonexistent/vite-xyz"}, Cwd: "/tmp", Ports: []string{"3000"}}
	if err := restartWhenFree(host, waitPoll); err == nil || !strings.Contains(err.Error(), "port 3000 still in use") {
		t.Errorf("held port: %v", err)
	}
	stubScans(t, nil)
	if err := restartWhenFree(host, waitPoll); err == nil || strings.Contains(err.Error(), "still in use") {
		t.Errorf("free port must reach the launch: %v", err)
	}
	withFakeDockerOK(t)
	ctr := Restartable{Name: "web", Container: true, Runtime: "docker", ID: "c1", Ports: []string{"3000"}}
	stubScans(t, []PortItem{{Port: "3000"}})
	if err := restartWhenFree(ctr, waitPoll); err != nil {
		t.Errorf("containers must not wait: %v", err)
	}
	if got := restartLabel(Restartable{Name: "node", Framework: "Vite"}); got != "Vite (node)" {
		t.Errorf("label: %q", got)
	}
	if got := restartLabel(Restartable{Name: "node"}); got != "node" {
		t.Errorf("label: %q", got)
	}
}

// withFakeDockerStartFails makes docker stop succeed and docker start fail.
func withFakeDockerStartFails(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker")
	sh := "#!/bin/sh\n[ \"$1\" = start ] && { echo nope >&2; exit 1; }\nexit 0\n"
	if err := os.WriteFile(path, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	stubDockerBin(t, path)
}

func TestRestartTargets(t *testing.T) {
	ctr := PortItem{Source: "docker", PID: "c1", Port: "80", Process: "docker:web"}
	ok := func(PortItem, bool) error { return nil }
	if _, err := restartTargets([]PortItem{ctr, {PID: "999999999", Process: "ghost"}}, false, ok, waitPoll, false); err == nil ||
		!strings.Contains(err.Error(), "ghost (PID 999999999)") || !strings.Contains(err.Error(), "nothing was stopped") {
		t.Errorf("unreadable target must refuse: %v", err)
	}
	me := strconv.Itoa(os.Getpid())
	stubProcTree(t, map[string]string{"/proc/" + me + "/environ": "pm_id=3\x00"})
	if _, err := restartTargets([]PortItem{{PID: me, Process: "app"}}, false, ok, waitPoll, false); err == nil || !strings.Contains(err.Error(), "managed by pm2") {
		t.Errorf("supervised target must refuse: %v", err)
	}
	res, err := restartTargets([]PortItem{ctr}, false, ok, waitPoll, true)
	if err != nil || len(res) != 1 || res[0].Status != "would-restart" || res[0].Command != "docker start web" {
		t.Errorf("dry run: %+v %v", res, err)
	}
	res, _ = restartTargets([]PortItem{ctr}, false, func(PortItem, bool) error { return errors.New("denied") }, waitPoll, false)
	if res[0].Status != "failed" || res[0].Error != "stop: denied" {
		t.Errorf("stop failure: %+v", res)
	}
	withFakeDockerStartFails(t)
	if res, _ = restartTargets([]PortItem{ctr}, false, ok, waitPoll, false); res[0].Status != "failed" {
		t.Errorf("start failure: %+v", res)
	}
	withFakeDockerOK(t)
	if res, _ = restartTargets([]PortItem{ctr}, false, ok, waitPoll, false); res[0].Status != "restarted" {
		t.Errorf("restart: %+v", res)
	}
}

func TestCLIRestart(t *testing.T) {
	ctr := PortItem{Source: "docker", PID: "c1", Port: "80", Process: "docker:web"}
	stubScans(t, []PortItem{ctr})
	withFakeDockerOK(t)
	code := stubExit(t)
	for args, want := range map[string]int{"--restart": 2, "--restart 80 --signal BOGUS": 2} {
		*code = 0
		captureStderr(t, func() { cliRestart(strings.Fields(args), false) })
		if *code != want {
			t.Errorf("%s: exit %d, want %d", args, *code, want)
		}
	}
	*code = 0
	out := captureStdout(t, func() { cliRestart([]string{"--restart", "80", "--dry-run"}, false) })
	if *code != 0 || !strings.Contains(out, "would restart docker:web on :80: docker start web") {
		t.Errorf("dry run: %d %q", *code, out)
	}
	out = captureStdout(t, func() { runCLI([]string{"--restart", "80", "--wait", "1"}) })
	if *code != 0 || !strings.Contains(out, "restarted docker:web on :80") {
		t.Errorf("restart: %d %q", *code, out)
	}
	errOut := captureStderr(t, func() { cliRestart([]string{"--restart", "81", "--kill-name", "ghost"}, false) })
	if *code != 1 || !strings.Contains(errOut, "nothing listening on port 81") || !strings.Contains(errOut, "no listener named ghost") {
		t.Errorf("missing: %d %q", *code, errOut)
	}
	withFakeDockerStartFails(t)
	*code = 0
	errOut = captureStderr(t, func() { cliRestart([]string{"--restart", "80"}, false) })
	if *code != 1 || !strings.Contains(errOut, "failed to restart docker:web") {
		t.Errorf("failure: %d %q", *code, errOut)
	}
	stubScans(t, []PortItem{{PID: "999999999", Port: "90", Process: "ghost"}})
	*code = 0
	errOut = captureStderr(t, func() { cliRestart([]string{"--restart", "90"}, false) })
	if *code != 1 || !strings.Contains(errOut, "cannot restart ghost") {
		t.Errorf("refusal: %d %q", *code, errOut)
	}
	if !cliNeedsRoot([]string{"--restart", "80"}) {
		t.Error("--restart must need root")
	}
}

func TestMCPRestartPort(t *testing.T) {
	ctr := PortItem{Source: "docker", PID: "c1", Port: "80", Process: "docker:web"}
	stubScans(t, []PortItem{ctr, {PID: "999999999", Port: "90", Process: "ghost"}})
	withFakeDockerOK(t)
	for args, want := range map[string]string{
		`{}`:                              "give ports",
		`{"ports":["0"]}`:                 "1..65535",
		`{"ports":["80"]}`:                "restarting is disabled",
		`{"ports":["90"],"dry_run":true}`: "cannot restart ghost",
	} {
		if text, isErr := callText(t, mcpServer{}, "restart_port", args); !isErr || !strings.Contains(text, want) {
			t.Errorf("%s: %v %q", args, isErr, text)
		}
	}
	text, isErr := callText(t, mcpServer{allowKill: true}, "restart_port", `{"ports":["80","81"]}`)
	if isErr || !strings.Contains(text, `"status": "restarted"`) || !strings.Contains(text, `"no_listener"`) {
		t.Errorf("restart: %v %s", isErr, text)
	}
	if text, _ := callText(t, mcpServer{}, "restart_port", `{"names":["web"],"dry_run":true}`); strings.Contains(text, "no_listener") {
		t.Errorf("all found: %s", text)
	}
}
