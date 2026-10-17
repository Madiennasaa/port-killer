package main

import "testing"

func TestCLINeedsRoot(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"--help"}, false},
		{[]string{"-h"}, false},
		{[]string{"--version"}, false},
		{[]string{"-v"}, false},
		{[]string{"--list"}, true},
		{[]string{"--list", "--json"}, true},
		{[]string{"--kill", "8765"}, true},
		{[]string{"--kill", "8765", "--force"}, true},
		{[]string{"--help", "--list"}, false},
		{[]string{"--version", "--list"}, false},
		{[]string{"--bogus"}, false},
	}
	for _, c := range cases {
		if got := cliNeedsRoot(c.args); got != c.want {
			t.Errorf("cliNeedsRoot(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestRunCLIHelpAndVersion(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"--version"}, {"-v"}} {
		if !runCLI(args) {
			t.Errorf("runCLI(%v) = false, want true", args)
		}
	}
}

func TestRunCLINoArgsStartsTUI(t *testing.T) {
	if runCLI(nil) {
		t.Fatal("runCLI(nil) = true, want false (TUI)")
	}
}

func TestRunCLIUnknownFlagExits(t *testing.T) {
	// runCLI calls os.Exit(2) on unknown flags; the exit path itself would
	// kill the test binary, so assert the classification here: unknown flags
	// are not root-bound, so main runs them before any sudo re-exec.
	if cliNeedsRoot([]string{"--nope"}) {
		t.Fatal("unknown flag must not be deferred behind sudo")
	}
}

func TestVersionStampedOrDev(t *testing.T) {
	if version == "" {
		t.Fatal("version must not be empty")
	}
}
