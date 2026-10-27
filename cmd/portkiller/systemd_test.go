package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestUnitFromCgroup(t *testing.T) {
	cases := []struct {
		in   string
		unit string
		ok   bool
	}{
		{"0::/system.slice/nginx.service\n", "nginx.service", true},
		{"12:pids:/system.slice/ssh.service\n", "ssh.service", true},
		{"0::/system.slice/ssh.service/child.scope\n", "", false},
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/dev.service\n", "", false},
		{"0::/init.scope\n", "", false},
		{"1:name=systemd:/system.slice/a.service\n0::/system.slice/b.service\n", "a.service", true},
		{"garbage", "", false},
		{"0::/system.slice/\n", "", false},
		{"0::/system.slice/notaservice\n", "", false},
	}
	for _, c := range cases {
		unit, ok := unitFromCgroup([]byte(c.in))
		if unit != c.unit || ok != c.ok {
			t.Errorf("unitFromCgroup(%q) = %q,%v want %q,%v", c.in, unit, ok, c.unit, c.ok)
		}
	}
}

func TestServiceUnit(t *testing.T) {
	old := procFile
	t.Cleanup(func() { procFile = old })

	procFile = func(string) ([]byte, error) { return []byte("0::/system.slice/nginx.service\n"), nil }
	if u, ok := serviceUnit(4242); !ok || u != "nginx.service" {
		t.Errorf("serviceUnit = %q,%v", u, ok)
	}
	procFile = func(string) ([]byte, error) { return nil, errors.New("gone") }
	if _, ok := serviceUnit(4242); ok {
		t.Error("unreadable cgroup must fail")
	}
}

// stubSystemctl installs a fake systemctl that records its argv and exits with
// the given code. When message is non-empty it is printed on stdout.
func stubSystemctl(t *testing.T, exit int, message string) string {
	t.Helper()
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$@\" > " + args + "\n"
	if message != "" {
		script += "echo " + strconv.Quote(message) + "\n"
	}
	script += "exit " + strconv.Itoa(exit) + "\n"
	path := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := systemctlBin
	systemctlBin = path
	t.Cleanup(func() { systemctlBin = old })
	return args
}

func TestSystemctlSuccess(t *testing.T) {
	args := stubSystemctl(t, 0, "")
	if err := systemctl("restart", "nginx.service"); err != nil {
		t.Fatalf("systemctl: %v", err)
	}
	b, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "restart nginx.service" {
		t.Errorf("argv = %q", b)
	}
}

func TestSystemctlFailureIncludesOutput(t *testing.T) {
	stubSystemctl(t, 1, "Unit nginx.service not found.")
	err := systemctl("restart", "nginx.service")
	if err == nil || !strings.Contains(err.Error(), "Unit nginx.service not found.") {
		t.Fatalf("error = %v, want the unit's message", err)
	}
}

func TestStopItemRoutesToSystemd(t *testing.T) {
	oldProc, oldStart := procFile, readStartTime
	oldSignal := sendSignal
	t.Cleanup(func() { procFile, readStartTime, sendSignal = oldProc, oldStart, oldSignal })

	procFile = func(p string) ([]byte, error) {
		if strings.HasSuffix(p, "/cgroup") {
			return []byte("0::/system.slice/nginx.service\n"), nil
		}
		return oldProc(p)
	}
	readStartTime = func(int) (uint64, bool) { return 1, true }
	sendSignal = func(int, syscall.Signal) error {
		t.Fatal("a systemd unit must not be signalled directly")
		return nil
	}
	args := stubSystemctl(t, 0, "")

	if err := stopItem(PortItem{PID: "123", Process: "nginx", startTime: 1}, false); err != nil {
		t.Fatalf("stopItem: %v", err)
	}
	b, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "stop nginx.service" {
		t.Errorf("argv = %q", b)
	}
}

func TestRestartOneSystemd(t *testing.T) {
	args := stubSystemctl(t, 0, "")
	if err := restartOne(Restartable{Name: "nginx", Unit: "nginx.service"}); err != nil {
		t.Fatalf("restartOne: %v", err)
	}
	b, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "restart nginx.service" {
		t.Errorf("argv = %q", b)
	}
}

func TestRestartDescSystemd(t *testing.T) {
	got := restartDesc(Restartable{Name: "nginx", Cmd: []string{"nginx"}, Unit: "nginx.service"})
	if !strings.Contains(got, "systemctl restart nginx.service") {
		t.Errorf("restartDesc = %q", got)
	}
}

func TestCaptureRestartUnit(t *testing.T) {
	old := procFile
	t.Cleanup(func() { procFile = old })
	procFile = func(p string) ([]byte, error) {
		if strings.HasSuffix(p, "/cgroup") {
			return []byte("0::/system.slice/nginx.service\n"), nil
		}
		return old(p)
	}
	r, ok := captureRestart(PortItem{PID: strconv.Itoa(os.Getpid()), Process: "nginx"})
	if !ok {
		t.Fatal("captureRestart failed")
	}
	if r.Unit != "nginx.service" {
		t.Errorf("Unit = %q, want nginx.service", r.Unit)
	}
}
