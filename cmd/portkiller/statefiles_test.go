package main

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// within fails the test if fn does not return in 3s (a FIFO open/read
// would block forever).
func within(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s hung", what)
	}
}

func mkfifo(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Skip("mkfifo:", err)
	}
}

// fakeRoot makes chownToSudoUser run (as ourselves) and records its paths.
func fakeRoot(t *testing.T) *[]string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Skip(err)
	}
	t.Setenv("SUDO_USER", u.Username)
	oldUID, oldChown := effectiveUID, chownSeam
	t.Cleanup(func() { effectiveUID, chownSeam = oldUID, oldChown })
	effectiveUID = func() int { return 0 }
	var got []string
	chownSeam = func(paths []string, uid, gid int) error {
		got = append(got, paths...)
		return chownPaths(paths, uid, gid)
	}
	return &got
}

// TestStateFIFOsDoNotHang: a FIFO planted where a state file or a project
// file is read or written must not block portkiller forever.
func TestStateFIFOsDoNotHang(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_USER", "")
	mkfifo(t, hiddenPath())
	mkfifo(t, guardPath())
	mkfifo(t, eventsPath())
	within(t, "loadHidden", func() { _ = loadHidden() })
	within(t, "loadGuard", func() { _ = loadGuard() })
	within(t, "loadEvents", func() { _ = loadEvents(eventsPath()) })
	within(t, "appendEvents", func() { appendEvents(eventsPath(), []Event{{Kind: "appeared", Port: "1"}}) })
	within(t, "saveHidden", func() { _ = saveHidden(hiddenPath(), map[string]bool{"x": true}) })
	if fi, err := os.Lstat(eventsPath()); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("events FIFO replaced or gone: %v", err)
	}

	proj := t.TempDir()
	mkfifo(t, filepath.Join(proj, "compose.yml"))
	mkfifo(t, filepath.Join(proj, ".env"))
	within(t, "readNeeds", func() { _ = readNeeds(proj) })
	mkfifo(t, filepath.Join(proj, ".git", "HEAD"))
	within(t, "gitBranch", func() { _ = gitBranch(proj, "") })
}

// TestGitHeadDeviceIsNotRead: a .git/HEAD symlinked to /dev/zero must not
// be read (os.ReadFile would grow memory forever).
func TestGitHeadDeviceIsNotRead(t *testing.T) {
	proj := t.TempDir()
	if err := os.Mkdir(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/zero", filepath.Join(proj, ".git", "HEAD")); err != nil {
		t.Fatal(err)
	}
	within(t, "gitBranch", func() {
		if b := gitBranch(proj, ""); b != "" {
			t.Errorf("branch = %q", b)
		}
	})
}

// TestReadRegularBounds: oversized files are cut at the cap.
func TestReadRegularBounds(t *testing.T) {
	old := maxStateFile
	t.Cleanup(func() { maxStateFile = old })
	maxStateFile = 64
	p := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(p, make([]byte, maxStateFile+10), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := readRegular(p)
	if err != nil || len(b) != maxStateFile {
		t.Fatalf("len=%d err=%v", len(b), err)
	}
	if _, err := readRegular(filepath.Join(p, "nope")); err == nil {
		t.Error("missing file must fail")
	}
}

// TestStateWritesSkipHardlinks: a state file hardlinked to another file
// must not let portkiller (as root) write into that file.
func TestStateWritesSkipHardlinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_USER", "")
	victim := filepath.Join(home, "victim")
	if err := os.WriteFile(victim, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{eventsPath(), hiddenPath()} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(victim, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(victim, eventsPath()+".lock"); err != nil {
		t.Fatal(err)
	}
	appendEvents(eventsPath(), []Event{{Kind: "appeared", Port: "1"}})
	_ = saveHidden(hiddenPath(), map[string]bool{"x": true})
	if lf := lockEvents(eventsPath(), true); lf != nil {
		unlockEvents(lf)
		t.Error("lock on a hardlinked file must be refused")
	}
	log := restartLogPath(Restartable{Name: "srv"})
	if err := os.Link(victim, log); err != nil {
		t.Fatal(err)
	}
	if _, err := defaultOpenRestartLog(log); err == nil {
		t.Error("hardlinked restart log must be refused")
	}
	if b, _ := os.ReadFile(victim); string(b) != "secret" {
		t.Fatalf("victim modified: %q", b)
	}
	if b, _ := os.ReadFile(hiddenPath()); !strings.Contains(string(b), "x\n") {
		t.Fatalf("hidden not replaced atomically: %q", b)
	}
}

// TestStateDirsCreatedAndOwned: under sudo, every directory portkiller
// creates in the user's home (not just the last one) goes to the user, and
// private state is 0700/0600. Paths are explicit: with SUDO_USER set,
// eventsPath() would resolve to the real home.
func TestStateDirsCreatedAndOwned(t *testing.T) {
	home := t.TempDir()
	chowned := fakeRoot(t)
	ev := filepath.Join(home, ".local", "state", "portkiller", "events.jsonl")
	appendEvents(ev, []Event{{Kind: "appeared", Port: "1"}})
	if _, err := os.Stat(ev); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(home, ".local"), filepath.Join(home, ".local", "state"), filepath.Dir(ev), ev, ev + ".lock"} {
		found := false
		for _, c := range *chowned {
			found = found || c == d
		}
		if !found {
			t.Errorf("%s not chowned (got %v)", d, *chowned)
		}
	}
	for _, c := range *chowned {
		if c == home {
			t.Error("pre-existing home must not be chowned")
		}
	}
	mode := func(p string) os.FileMode { fi, _ := os.Stat(p); return fi.Mode().Perm() }
	if m := mode(filepath.Dir(ev)); m != 0o700 {
		t.Errorf("state dir mode %o", m)
	}
	if m := mode(ev); m != 0o600 {
		t.Errorf("events mode %o", m)
	}
	if m := mode(ev + ".lock"); m != 0o600 {
		t.Errorf("lock mode %o", m)
	}
	log := filepath.Join(filepath.Dir(ev), "restart-srv.log")
	f, err := defaultOpenRestartLog(log)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if m := mode(log); m != 0o600 {
		t.Errorf("restart log mode %o", m)
	}
	*chowned = nil
	hidden := filepath.Join(home, ".config", "portkiller", "hidden")
	if err := saveHidden(hidden, map[string]bool{"x": true}); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(*chowned, " ")
	if !strings.Contains(got, filepath.Join(home, ".config")+" ") || !strings.Contains(got, ".hidden.tmp") {
		t.Errorf("hidden dirs/temp not chowned: %v", *chowned)
	}
	if m := mode(hidden); m != 0o644 {
		t.Errorf("hidden mode %o", m)
	}
}

// TestRestartLogBounded: a restart log over the cap starts over instead of
// growing forever across restarts.
func TestRestartLogBounded(t *testing.T) {
	p := filepath.Join(t.TempDir(), "restart-x.log")
	if err := os.WriteFile(p, make([]byte, maxRestartLog+1), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := defaultOpenRestartLog(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if fi, _ := os.Stat(p); fi.Size() != 0 {
		t.Fatalf("size %d, want truncated", fi.Size())
	}
	small := filepath.Join(filepath.Dir(p), "restart-y.log")
	_ = os.WriteFile(small, []byte("keep\n"), 0o600)
	f, _ = defaultOpenRestartLog(small)
	f.Close()
	if b, _ := os.ReadFile(small); string(b) != "keep\n" {
		t.Fatalf("small log changed: %q", b)
	}
}

// TestEventsTrimIsAtomic: trimming rewrites the file through a rename, so
// the result is a fresh single-link file with only the newest lines.
func TestEventsTrimIsAtomic(t *testing.T) {
	p := filepath.Join(t.TempDir(), "events.jsonl")
	var sb strings.Builder
	for i := 0; i < maxEvents+250; i++ {
		sb.WriteString(`{"t":"2026-01-01T00:00:00Z","kind":"appeared","port":"1"}` + "\n")
	}
	if err := os.WriteFile(p, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	appendEvents(p, []Event{{Kind: "appeared", Port: "2"}})
	b, _ := os.ReadFile(p)
	if n := strings.Count(string(b), "\n"); n != maxEvents {
		t.Fatalf("lines = %d", n)
	}
	if !strings.Contains(string(b), `"port":"2"`) {
		t.Fatal("newest event lost")
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}

// TestWriteFileAtomicRefusesNonRegular: a symlink or directory target is
// refused and no temp file is left behind.
func TestWriteFileAtomicRefusesNonRegular(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	_ = os.Symlink(filepath.Join(dir, "elsewhere"), link)
	if err := writeFileAtomic(link, []byte("x"), 0o644); err == nil {
		t.Error("symlink target must be refused")
	}
	if err := writeFileAtomic(dir, []byte("x"), 0o644); err == nil {
		t.Error("directory target must be refused")
	}
	if _, err := os.Lstat(filepath.Join(dir, "elsewhere")); err == nil {
		t.Error("write followed the symlink")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("leftovers: %v", ents)
	}
}
