package main

import (
	"slices"
	"testing"
)

// TestReexecSudoMarksAndStopsLoops: the re-exec carries a marker, and a
// process that already went through sudo but is still not root (a sudo
// shim, runas_default, a container) must not exec sudo again forever.
func TestReexecSudoMarksAndStopsLoops(t *testing.T) {
	oldExe, oldLook, oldExec := executable, lookPath, sysExec
	t.Cleanup(func() { executable, lookPath, sysExec = oldExe, oldLook, oldExec })
	executable = func() (string, error) { return "/bin/true", nil }
	lookPath = func(string) (string, error) { return "/usr/bin/sudo", nil }
	var gotEnv []string
	calls := 0
	sysExec = func(_ string, _ []string, env []string) error { calls++; gotEnv = env; return nil }

	t.Setenv(sudoMarker, "")
	reexecSudo()
	if calls != 1 || !slices.Contains(gotEnv, sudoMarker+"=1") {
		t.Fatalf("calls=%d env lacks marker", calls)
	}
	t.Setenv(sudoMarker, "1")
	reexecSudo()
	if calls != 1 {
		t.Fatal("re-exec looped although the marker was set")
	}
}
