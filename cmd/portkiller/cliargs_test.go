package main

import (
	"strings"
	"syscall"
	"testing"
)

// runCLIExit runs runCLI with exit and the scan stubbed, returning the exit
// code (0 = none), stderr, and whether anything was stopped.
func runCLIExit(t *testing.T, args ...string) (code int, stderr string, stopped bool) {
	t.Helper()
	withTempHome(t)
	// A PID above any pid_max (4194304) cannot exist, and the signal seam
	// is stubbed: nothing real is ever signalled.
	stubActivePorts(t, []PortItem{{Proto: "tcp", Port: "8080", PID: "99999999", Process: "node", Source: "host"}})
	old, oldSig, oldGone := exitProcess, sendSignal, procGone
	t.Cleanup(func() { exitProcess, sendSignal, procGone = old, oldSig, oldGone })
	procGone = func(int) bool { return true }
	exitProcess = func(c int) {
		if code == 0 {
			code = c
		}
	}
	sendSignal = func(int, syscall.Signal) error { stopped = true; return nil }
	stderr = captureStderr(t, func() { captureStdout(t, func() { runCLI(args) }) })
	return code, stderr, stopped
}

// TestCLIRejectsBadArgs: bad values, conflicting modes, unknown flags next
// to a mode, and modifiers without their mode exit 2 with a clear message
// and never stop anything.
func TestCLIRejectsBadArgs(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--kill", "abc"}, `bad port "abc"`},
		{[]string{"--kill", "0"}, "out of range"},
		{[]string{"--kill", "8080", "-9"}, "unknown flag -9"},
		{[]string{"--restart", "70000"}, "out of range"},
		{[]string{"--list", "--kill", "8080"}, "--list and --kill cannot be combined"},
		{[]string{"--free", "3000-3010", "--kill", "8080"}, "--free and --kill cannot be combined"},
		{[]string{"--watch", "--history"}, "--history and --watch cannot be combined"},
		{[]string{"--restart", "8080", "--kill", "8080"}, "--restart and --kill cannot be combined"},
		{[]string{"--list", "--jsn"}, "unknown flag --jsn"},
		{[]string{"--kill", "8080", "--froce"}, "unknown flag --froce"},
		{[]string{"--list", "-j"}, "unknown flag -j"},
		{[]string{"--list", "--json", "--plain"}, "--json and --plain cannot be combined"},
		{[]string{"--json"}, "--json needs one of --list, --free, --history"},
		{[]string{"--list", "--force"}, "--force needs one of --kill, --kill-name, --restart"},
		{[]string{"--history", "--notify"}, "--notify needs --watch"},
		{[]string{"--list", "--allow-kill"}, "--allow-kill needs --mcp"},
	}
	for _, c := range cases {
		code, stderr, stopped := runCLIExit(t, c.args...)
		if code != 2 || !strings.Contains(stderr, c.want) || stopped {
			t.Errorf("%v: code=%d stopped=%v stderr=%q, want exit 2 with %q", c.args, code, stopped, stderr, c.want)
		}
	}
}

// TestCLIKillNormalizesPorts: "08080" means port 8080.
func TestCLIKillNormalizesPorts(t *testing.T) {
	code, stderr, stopped := runCLIExit(t, "--kill", "08080")
	if code != 0 || !stopped {
		t.Fatalf("code=%d stopped=%v stderr=%q", code, stopped, stderr)
	}
}

// TestCLIValidArgsStillWork: allowed combinations are not rejected, and a
// --kill-name value may look like a flag.
func TestCLIValidArgsStillWork(t *testing.T) {
	for _, args := range [][]string{
		{"--kill", "8080", "--kill-name", "-weird", "--force", "--dry-run"},
		{"--list", "--json"},
		{"--history", "--port", "8080", "--limit", "5", "--json"},
		{"--help", "--list", "--kill", "1"},
	} {
		if code, stderr, _ := runCLIExit(t, args...); code == 2 {
			t.Errorf("%v rejected: %q", args, stderr)
		}
	}
}

// TestCLINeedsRootSkipsInvalidArgs: invalid args fail before the sudo
// re-exec instead of asking for a password first.
func TestCLINeedsRootSkipsInvalidArgs(t *testing.T) {
	if cliNeedsRoot([]string{"--list", "--jsn"}) {
		t.Error("invalid args must not need root")
	}
	if !cliNeedsRoot([]string{"--list"}) {
		t.Error("--list needs root")
	}
}

// TestCLIRestartScanWarning: a failed scan is reported, and with nothing
// found the restart fails.
func TestCLIRestartScanWarning(t *testing.T) {
	old, oldExit := activePorts, exitProcess
	t.Cleanup(func() { activePorts, exitProcess = old, oldExit })
	activePorts = func() ([]PortItem, error) { return nil, errTest }
	code := 0
	exitProcess = func(c int) { code = c }
	errOut := captureStderr(t, func() { cliRestart([]string{"--restart", "80"}, false) })
	if code != 1 || !strings.Contains(errOut, "warning:") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}
