package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRestartHostKeepsArgv0: when the captured /proc exe is used, the
// relaunched process must still get its original argv[0]. Multi-call
// binaries (busybox, some node/python launchers) dispatch on it; before,
// argv[0] became the exe path.
func TestRestartHostKeepsArgv0(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	withTempHome(t)
	out := filepath.Join(t.TempDir(), "argv")
	r := Restartable{
		Name: "argv0-test",
		Exe:  py,
		Cmd:  []string{"custom-argv0", "-c", "import sys; open(sys.argv[1], 'w').write(open('/proc/self/cmdline').read())", out},
		Cwd:  t.TempDir(),
		UID:  os.Getuid(),
	}
	if err := restartHost(r); err != nil {
		t.Fatal(err)
	}
	var b []byte
	for i := 0; i < 100 && len(b) == 0; i++ {
		time.Sleep(30 * time.Millisecond)
		b, _ = os.ReadFile(out)
	}
	if argv0, _, _ := strings.Cut(string(b), "\x00"); argv0 != "custom-argv0" {
		t.Fatalf("argv[0] = %q, want custom-argv0 (cmdline %q)", argv0, b)
	}
}
