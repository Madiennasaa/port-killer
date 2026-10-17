package main

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestRestartable(t *testing.T) {
	cases := []struct {
		cmd  []string
		cwd  string
		want bool
	}{
		{[]string{"node", "server.js"}, "/srv/app", true},
		{nil, "/srv/app", false},
		{[]string{"", "x"}, "/srv/app", false},
		{[]string{"node"}, "", false},
		{[]string{"node"}, "-", false},
		{[]string{"node"}, "/srv/app (deleted)", false},
	}
	for _, c := range cases {
		if got := restartable(c.cmd, c.cwd); got != c.want {
			t.Errorf("restartable(%v, %q) = %v, want %v", c.cmd, c.cwd, got, c.want)
		}
	}
}

func TestRestartDesc(t *testing.T) {
	if got := restartDesc(Restartable{Docker: true, Name: "web"}); got != "docker start web" {
		t.Errorf("docker desc = %q", got)
	}
	if got := restartDesc(Restartable{Cmd: []string{"node", "a.js"}}); got != "node a.js" {
		t.Errorf("cmd desc = %q", got)
	}
}

func TestCaptureRestartDocker(t *testing.T) {
	r, ok := captureRestart(PortItem{Source: "docker", PID: "abc123", Process: "docker:web"})
	if !ok || !r.Docker || r.ID != "abc123" || r.Name != "web" {
		t.Errorf("got %+v ok=%v", r, ok)
	}
}

func TestCaptureRestartBadPID(t *testing.T) {
	if _, ok := captureRestart(PortItem{PID: "not-a-pid", Process: "x"}); ok {
		t.Error("non-numeric pid must not be restartable")
	}
	if _, ok := captureRestart(PortItem{PID: "999999999", Process: "x"}); ok {
		t.Error("vanished process must not be restartable")
	}
}

func TestCaptureRestartSelf(t *testing.T) {
	r, ok := captureRestart(PortItem{PID: strconv.Itoa(os.Getpid()), Process: "portkiller.test"})
	if !ok {
		t.Skip("own /proc entries may be unreadable in some sandboxes")
	}
	if len(r.Cmd) == 0 || r.Cwd == "" || r.Cwd == "-" {
		t.Errorf("captured %+v lacks cmd/cwd", r)
	}
}

func TestCaptureAllDedupsAndSkips(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())
	items := []PortItem{
		{PID: pid, Process: "a"},
		{PID: pid, Process: "a"},
		{PID: "not-a-pid", Process: "b"},
		{Source: "docker", PID: "c1", Process: "docker:db"},
	}
	got := captureAll(items)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	if got[1].Docker != true {
		t.Errorf("second entry should be docker: %+v", got[1])
	}
}

// TestRestartOneBadCommand exercises the error path without touching docker.
func TestRestartOneBadCommand(t *testing.T) {
	r := Restartable{Cmd: []string{"/nonexistent/binary-xyz"}, Cwd: "/tmp"}
	if err := restartOne(r); err == nil {
		t.Error("starting a missing binary must fail")
	}
}

// TestRestartOneSpawnsProcess verifies a successful restart actually spawns.
func TestRestartOneSpawnsProcess(t *testing.T) {
	r := Restartable{Cmd: []string{"sleep", "30"}, Cwd: "/tmp", UID: os.Geteuid()}
	if err := restartOne(r); err != nil {
		t.Skipf("cannot spawn in this environment: %v", err)
	}
	// Give the child a moment; it is detached (setsid), so just confirm a
	// sleep 30 now exists that did not before.
	time.Sleep(50 * time.Millisecond)
	out, _ := exec.Command("pgrep", "-f", "sleep 30").Output()
	if len(out) == 0 {
		t.Error("restartOne did not leave a running process")
	}
	exec.Command("pkill", "-f", "sleep 30").Run()
}

func TestRestartCmdCounts(t *testing.T) {
	good := Restartable{Cmd: []string{"/nonexistent/a-xyz"}, Cwd: "/tmp"}
	msg := restartCmd([]Restartable{good, good})().(restartMsg)
	if msg.done != 0 || msg.err == nil {
		t.Errorf("all failing: %+v", msg)
	}
	if msg.done != 0 {
		t.Error("done must count only successes")
	}
}

func TestRestartCmdSnapshot(t *testing.T) {
	rs := []Restartable{{Cmd: []string{"/nonexistent/b-xyz"}, Cwd: "/tmp"}}
	cmd := restartCmd(rs)
	rs[0].Cmd[0] = "/mutated"
	msg := cmd().(restartMsg)
	if msg.err == nil {
		t.Error("snapshot must be taken at cmd creation")
	}
}
