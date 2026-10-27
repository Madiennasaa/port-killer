package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExecFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}

type findInPathFixture struct {
	dir string
	bin string
}

func newFindInPathFixture(t *testing.T) findInPathFixture {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "tool")
	writeExecFile(t, bin, 0o755)
	writeExecFile(t, filepath.Join(dir, "noexec"), 0o644)
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	return findInPathFixture{dir: dir, bin: bin}
}

func TestFindInPathFindsExecutable(t *testing.T) {
	f := newFindInPathFixture(t)
	got, err := findInPath("tool", f.dir)
	if err != nil || got != f.bin {
		t.Errorf("findInPath(tool) = %q, %v; want %q", got, err, f.bin)
	}
	// Empty PATH entries are skipped, not treated as the cwd.
	got, err = findInPath("tool", string(os.PathListSeparator)+f.dir)
	if err != nil || got != f.bin {
		t.Errorf("findInPath with empty entry = %q, %v", got, err)
	}
}

func TestFindInPathRejectsNonBinaries(t *testing.T) {
	f := newFindInPathFixture(t)
	for _, name := range []string{"noexec", "adir", "missing"} {
		if _, err := findInPath(name, f.dir); err == nil {
			t.Errorf("findInPath(%q) must fail", name)
		}
	}
}

func TestFindInPathKeepsExplicitPath(t *testing.T) {
	if got, err := findInPath("/bin/sh", t.TempDir()); err != nil || got != "/bin/sh" {
		t.Errorf("absolute name = %q, %v", got, err)
	}
}

func TestResolveBinFallsBackToName(t *testing.T) {
	const name = "portkiller-no-such-binary-xyz"
	if got := resolveBin(name); got != name {
		t.Errorf("resolveBin(%q) = %q, want the bare name", name, got)
	}
}

func TestResolveBinIgnoresCallerPath(t *testing.T) {
	dir := t.TempDir()
	writeExecFile(t, filepath.Join(dir, "docker"), 0o755)
	t.Setenv("PATH", dir)

	if got := resolveBin("docker"); strings.HasPrefix(got, dir) {
		t.Fatalf("resolveBin used the caller PATH: %q", got)
	}
}

func TestHardenedEnvForcesSafePath(t *testing.T) {
	t.Setenv("PATH", "/tmp/evil-path")
	t.Setenv("PORTKILLER_KEEP_ME", "yes")

	env := hardenedEnv()
	var paths []string
	kept := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			paths = append(paths, strings.TrimPrefix(kv, "PATH="))
		}
		if kv == "PORTKILLER_KEEP_ME=yes" {
			kept = true
		}
	}
	if len(paths) != 1 || paths[0] != safePath {
		t.Errorf("PATH entries = %v, want one %q", paths, safePath)
	}
	if !kept {
		t.Error("unrelated environment variables must be preserved")
	}
}

func TestDefaultBinsResolved(t *testing.T) {
	for name, bin := range map[string]string{"ss": ssBin, "ps": psBin, "docker": dockerBin} {
		if bin == "" {
			t.Errorf("%s binary path is empty", name)
		}
	}
}
