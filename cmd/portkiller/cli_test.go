package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

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

func TestRunCLIListAndJSON(t *testing.T) {
	if !runCLI([]string{"--list"}) {
		t.Fatal("--list should be handled")
	}
	if !runCLI([]string{"--list", "--json"}) {
		t.Fatal("--list --json should be handled")
	}
}

// TestRunCLIExitCodes re-execs the test binary so the os.Exit paths in runCLI
// can be observed without killing the test run itself.
func TestRunCLIExitCodes(t *testing.T) {
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"--bogus"}, 2},
		{[]string{"--kill"}, 2},
		{[]string{"--kill", "notaport"}, 1}, // parses fine, nothing listens
		{[]string{"--kill", "1"}, 1},        // nothing listens on port 1
	}
	for _, c := range cases {
		cmd := exec.Command(os.Args[0], "-test.run=TestRunCLIHelperProcess", "--")
		cmd.Env = append(os.Environ(), "PORTKILLER_CLI_HELPER=1",
			"PORTKILLER_CLI_ARGS="+strings.Join(c.args, " "))
		err := cmd.Run()
		exit, ok := exitCode(err)
		if !ok {
			t.Fatalf("args %v: helper did not exit: %v", c.args, err)
		}
		if exit != c.want {
			t.Errorf("args %v: exit = %d, want %d", c.args, exit, c.want)
		}
	}
}

// TestRunCLIHelperProcess is not a real test: it is the subprocess entry point
// for TestRunCLIExitCodes.
func TestRunCLIHelperProcess(t *testing.T) {
	if os.Getenv("PORTKILLER_CLI_HELPER") != "1" {
		t.Skip("helper process only")
	}
	args := strings.Fields(os.Getenv("PORTKILLER_CLI_ARGS"))
	runCLI(args)
	os.Exit(0)
}

func exitCode(err error) (int, bool) {
	if err == nil {
		return 0, true
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), true
	}
	return 0, false
}
