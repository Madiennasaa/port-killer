package main

import (
	"context"
	"strings"
	"testing"
)

// A malformed value must be rejected before the sudo prompt: cliNeedsRoot
// said true for "--kill notaport", so the user typed a password only to be
// told the port was bad (and without a terminal, sudo failed with exit 1
// instead of the documented 2).
func TestBadValuesRejectedBeforeSudo(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--kill", "notaport"}, `bad port "notaport"`},
		{[]string{"--restart", "0"}, "out of range"},
		{[]string{"--kill", "80", "--signal", "BOGUS"}, "unknown signal"},
		{[]string{"--kill", "80", "--signal"}, "--signal needs"},
		{[]string{"--kill", "80", "--signal", "HUP", "--force"}, "--signal and --force"},
		{[]string{"--kill-name", "node", "--wait", "x"}, "--wait needs whole seconds"},
		{[]string{"--restart", "80", "--wait", "0"}, "--wait needs whole seconds"},
		{[]string{"--watch", "--port", "99999"}, "--port must be 1..65535"},
	} {
		if got := cliArgError(c.args); !strings.Contains(got, c.want) {
			t.Errorf("%v: cliArgError=%q, want %q", c.args, got, c.want)
		}
		if cliNeedsRoot(c.args) {
			t.Errorf("%v: must not ask for sudo", c.args)
		}
	}
	for _, ok := range [][]string{
		{"--kill", "08080", "--signal", "HUP", "--wait", "3"},
		{"--kill-name", "node"},
		{"--restart", "80", "--wait"},
		{"--watch", "--port", "80"},
		{"--watch"},
		{"--list"},
	} {
		if got := cliArgError(ok); got != "" || !cliNeedsRoot(ok) {
			t.Errorf("%v: rejected %q", ok, got)
		}
	}
}

// cliKill and cliRestart still refuse bad ports when called directly, in
// case a future caller skips cliArgError.
func TestKillRestartRecheckPorts(t *testing.T) {
	withTempHome(t)
	old := exitProcess
	t.Cleanup(func() { exitProcess = old })
	var code int
	exitProcess = func(c int) { code = c }
	for _, run := range []func(){
		func() { cliKill([]string{"--kill", "abc"}, false) },
		func() { cliRestart([]string{"--restart", "abc"}, false) },
	} {
		code = 0
		if errOut := captureStderr(t, run); code != 2 || !strings.Contains(errOut, `bad port "abc"`) {
			t.Errorf("code=%d stderr=%q", code, errOut)
		}
	}
}

// TestRunCLIDispatchesWatch: a valid --watch reaches cliWatch through runCLI
// (the bad-port path now stops in cliArgError).
func TestRunCLIDispatchesWatch(t *testing.T) {
	withTempHome(t)
	stubActivePorts(t, nil)
	oldCtx := newWatchCtx
	t.Cleanup(func() { newWatchCtx = oldCtx })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	newWatchCtx = func() context.Context { return ctx }
	var handled bool
	errOut := captureStderr(t, func() { captureStdout(t, func() { handled = runCLI([]string{"--watch", "--port", "80"}) }) })
	if !handled || !strings.Contains(errOut, "watching ports") {
		t.Errorf("handled=%v stderr=%q", handled, errOut)
	}
}
