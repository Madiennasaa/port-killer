package main

import (
	"net"
	"strings"
	"testing"
)

func TestParsePortRange(t *testing.T) {
	cases := []struct {
		in         string
		start, end int
		ok         bool
	}{
		{"3000-3010", 3000, 3010, true},
		{"3000", 3000, 3000, true},
		{"1-65535", 1, 65535, true},
		{"0-1", 0, 0, false},
		{"1-70000", 0, 0, false},
		{"10-5", 0, 0, false},
		{"", 0, 0, false},
		{"abc", 0, 0, false},
		{"1-2-3", 0, 0, false},
		{"-5", 0, 0, false},
		{"5-", 0, 0, false},
	}
	for _, c := range cases {
		start, end, err := parsePortRange(c.in)
		if (err == nil) != c.ok || start != c.start || end != c.end {
			t.Errorf("parsePortRange(%q) = %d,%d,%v; want %d,%d,ok=%v",
				c.in, start, end, err, c.start, c.end, c.ok)
		}
	}
}

func TestFreeArgs(t *testing.T) {
	cases := []struct {
		args []string
		spec string
		ok   bool
	}{
		{[]string{"--free", "3000-3010"}, "3000-3010", true},
		{[]string{"--free"}, "", false},
		{[]string{"--free", "--json"}, "", false},
		{[]string{"x", "--free", "80"}, "80", true},
	}
	for _, c := range cases {
		spec, ok := freeArgs(c.args)
		if spec != c.spec || ok != c.ok {
			t.Errorf("freeArgs(%v) = %q,%v want %q,%v", c.args, spec, ok, c.spec, c.ok)
		}
	}
}

func TestFirstFreePortSkipsTaken(t *testing.T) {
	old := portFree
	t.Cleanup(func() { portFree = old })
	taken := map[int]bool{4000: true, 4001: true}
	portFree = func(p int) bool { return !taken[p] }

	got, ok := firstFreePort(4000, 4002)
	if !ok || got != 4002 {
		t.Errorf("firstFreePort = %d,%v want 4002", got, ok)
	}
	if _, ok := firstFreePort(4000, 4001); ok {
		t.Error("a fully taken range must report no free port")
	}
}

func TestPortFreeRealSocket(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind a local port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if portFree(port) {
		t.Errorf("port %d is bound but reported free", port)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if !portFree(port) {
		t.Errorf("port %d should be free after close", port)
	}
}

func TestCLIFreePlainAndJSON(t *testing.T) {
	old := portFree
	t.Cleanup(func() { portFree = old })
	portFree = func(p int) bool { return p == 4000 }

	out := captureStdout(t, func() { cliFree([]string{"--free", "3990-4010"}, false) })
	if strings.TrimSpace(out) != "4000" {
		t.Errorf("plain output = %q", out)
	}
	out = captureStdout(t, func() { cliFree([]string{"--free", "3990-4010"}, true) })
	if strings.TrimSpace(out) != `{"port":4000}` {
		t.Errorf("json output = %q", out)
	}
}

func TestCLIFreeExitCodes(t *testing.T) {
	old := portFree
	oldExit := exitProcess
	t.Cleanup(func() { portFree, exitProcess = old, oldExit })
	portFree = func(int) bool { return false }

	var code int
	exitProcess = func(c int) { code = c }

	cliFree([]string{"--free", "nope"}, false)
	if code != 2 {
		t.Errorf("bad range exit = %d, want 2", code)
	}
	cliFree([]string{"--free"}, false)
	if code != 2 {
		t.Errorf("missing range exit = %d, want 2", code)
	}
	cliFree([]string{"--free", "4000-4001"}, false)
	if code != 1 {
		t.Errorf("no free port exit = %d, want 1", code)
	}
}

func TestRunCLIFree(t *testing.T) {
	old := portFree
	t.Cleanup(func() { portFree = old })
	portFree = func(p int) bool { return p == 4321 }

	out := captureStdout(t, func() {
		if !runCLI([]string{"--free", "4321"}) {
			t.Error("--free must be handled by the CLI")
		}
	})
	if strings.TrimSpace(out) != "4321" {
		t.Errorf("output = %q", out)
	}
	if cliNeedsRoot([]string{"--free", "4321"}) {
		t.Error("--free must not require root")
	}
	if cliNeedsRoot([]string{"--json"}) {
		t.Error("--json alone must not require root")
	}
}
