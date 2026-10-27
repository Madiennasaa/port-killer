package main

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// isolateHome keeps restart logs out of the real home directory.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SUDO_USER", "")
}

func TestCaptureRestartExeAndEnv(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.Env = append(os.Environ(), "PORTKILLER_CAPTURE_ENV=yes")
	if err := cmd.Start(); err != nil {
		t.Skip("sleep tidak tersedia")
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	r, ok := captureRestart(PortItem{PID: strconv.Itoa(cmd.Process.Pid), Process: "sleep"})
	if !ok {
		t.Skip("child /proc entries may be unreadable in some sandboxes")
	}
	if r.Exe == "" || !filepath.IsAbs(r.Exe) {
		t.Errorf("Exe = %q, want an absolute path", r.Exe)
	}
	found := false
	for _, kv := range r.Env {
		if kv == "PORTKILLER_CAPTURE_ENV=yes" {
			found = true
		}
	}
	if !found {
		t.Errorf("captured env misses PORTKILLER_CAPTURE_ENV: %v", r.Env)
	}
}

func TestCaptureRestartExeReadError(t *testing.T) {
	old := readProcLink
	t.Cleanup(func() { readProcLink = old })
	readProcLink = func(p string) (string, error) {
		if strings.HasSuffix(p, "/exe") {
			return "", errors.New("denied")
		}
		return t.TempDir(), nil
	}
	r, ok := captureRestart(PortItem{PID: strconv.Itoa(os.Getpid()), Process: "x"})
	if !ok {
		t.Fatal("a readable cmdline/cwd must stay restartable")
	}
	if r.Exe != "" {
		t.Errorf("Exe = %q, want empty on readlink failure", r.Exe)
	}
}

func TestCaptureRestartDeletedExe(t *testing.T) {
	old := readProcLink
	t.Cleanup(func() { readProcLink = old })
	readProcLink = func(p string) (string, error) {
		if strings.HasSuffix(p, "/exe") {
			return "/tmp/gone (deleted)", nil
		}
		return t.TempDir(), nil
	}
	r, ok := captureRestart(PortItem{PID: strconv.Itoa(os.Getpid()), Process: "x"})
	if !ok {
		t.Fatal("a deleted exe must not block restart")
	}
	if r.Exe != "" {
		t.Errorf("Exe = %q, want empty for a deleted binary", r.Exe)
	}
}

func TestCaptureRestartEnvReadError(t *testing.T) {
	old := procFile
	t.Cleanup(func() { procFile = old })
	procFile = func(p string) ([]byte, error) {
		if strings.HasSuffix(p, "/environ") {
			return nil, errors.New("denied")
		}
		return old(p)
	}
	r, ok := captureRestart(PortItem{PID: strconv.Itoa(os.Getpid()), Process: "x"})
	if !ok {
		t.Fatal("a missing environ must not block restart")
	}
	if len(r.Env) != 0 {
		t.Errorf("Env = %v, want empty", r.Env)
	}
}

func TestSafeFileName(t *testing.T) {
	cases := map[string]string{
		"docker:web/1": "docker_web_1",
		"node":         "node",
		"":             "process",
		"a b\tc":       "a_b_c",
	}
	for in, want := range cases {
		if got := safeFileName(in); got != want {
			t.Errorf("safeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func writeRestartScript(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRestartOneUsesCapturedExe(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	exe := writeRestartScript(t, dir, "echo ran > "+marker)

	r := Restartable{Name: "demo", Exe: exe, Cmd: []string{"/nonexistent/impostor"}, Cwd: dir, UID: os.Geteuid()}
	if err := restartOne(r); err != nil {
		t.Fatalf("restartOne: %v", err)
	}
	waitForFile(t, marker)
}

func TestRestartOneUsesCapturedEnv(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "env.txt")
	exe := writeRestartScript(t, dir, `echo "$PORTKILLER_RESTART_ENV" > `+marker)

	r := Restartable{
		Name: "demo", Exe: exe, Cmd: []string{"sh"}, Cwd: dir, UID: os.Geteuid(),
		Env: []string{"PORTKILLER_RESTART_ENV=restored"},
	}
	if err := restartOne(r); err != nil {
		t.Fatalf("restartOne: %v", err)
	}
	waitForContent(t, marker, "restored")
}

func TestRestartOneWritesLog(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	exe := writeRestartScript(t, dir, `echo hello-from-restart`)

	r := Restartable{Name: "demo", Exe: exe, Cmd: []string{"sh"}, Cwd: dir, UID: os.Geteuid()}
	if err := restartOne(r); err != nil {
		t.Fatalf("restartOne: %v", err)
	}
	waitForContent(t, restartLogPath(r), "hello-from-restart")
}

func TestRestartOneLogOpenFailureStillStarts(t *testing.T) {
	isolateHome(t)
	old := openRestartLog
	t.Cleanup(func() { openRestartLog = old })
	openRestartLog = func(string) (*os.File, error) { return nil, errors.New("readonly") }

	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	exe := writeRestartScript(t, dir, "echo ok > "+marker)
	r := Restartable{Name: "demo", Exe: exe, Cmd: []string{"sh"}, Cwd: dir, UID: os.Geteuid()}
	if err := restartOne(r); err != nil {
		t.Fatalf("a log failure must not block the restart: %v", err)
	}
	waitForFile(t, marker)
}

func TestRestartLogPathUsesEventsDir(t *testing.T) {
	isolateHome(t)
	got := restartLogPath(Restartable{Name: "demo"})
	want := filepath.Join(filepath.Dir(eventsPath()), "restart-demo.log")
	if got != want {
		t.Errorf("restartLogPath = %q, want %q", got, want)
	}
}

func TestDefaultOpenRestartLogErrors(t *testing.T) {
	base := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(base, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := defaultOpenRestartLog(filepath.Join(base, "sub", "x.log")); err == nil {
		t.Error("MkdirAll failure must surface")
	}
	if _, err := defaultOpenRestartLog(t.TempDir()); err == nil {
		t.Error("opening a directory as a log must fail")
	}
}

func TestRestartOneCredentialErrorsCloseLog(t *testing.T) {
	isolateHome(t)
	oldUID, oldLookup := effectiveUID, lookupUserById
	effectiveUID = func() int { return 0 }
	lookupUserById = func(string) (*user.User, error) { return nil, errors.New("no user") }
	t.Cleanup(func() { effectiveUID, lookupUserById = oldUID, oldLookup })

	r := Restartable{Name: "demo", Cmd: []string{"true"}, Cwd: t.TempDir(), UID: 12345}
	if err := restartOne(r); err == nil {
		t.Fatal("unknown uid must fail")
	}

	lookupUserById = func(string) (*user.User, error) {
		return &user.User{Username: "ghost", Gid: "bad", HomeDir: "/tmp"}, nil
	}
	if err := restartOne(r); err == nil {
		t.Fatal("bad gid must fail")
	}
}

func TestRestartOneStartErrorClosesLog(t *testing.T) {
	isolateHome(t)
	r := Restartable{
		Name: "demo",
		Exe:  filepath.Join(t.TempDir(), "missing"),
		Cmd:  []string{"missing"},
		Cwd:  t.TempDir(),
		UID:  os.Geteuid(),
	}
	if err := restartOne(r); err == nil {
		t.Fatal("a missing exe must fail")
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	waitForContent(t, path, "")
}

func waitForContent(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	b, err := os.ReadFile(path)
	t.Fatalf("%s not ready: %q, %v", path, b, err)
}

func TestCapturedExeEmptyIsSkipped(t *testing.T) {
	old := readProcLink
	t.Cleanup(func() { readProcLink = old })
	readProcLink = func(p string) (string, error) {
		if strings.HasSuffix(p, "/exe") {
			return "", nil
		}
		return t.TempDir(), nil
	}
	r, ok := captureRestart(PortItem{PID: strconv.Itoa(os.Getpid()), Process: "x"})
	if !ok {
		t.Fatal("captureRestart failed")
	}
	if r.Exe != "" {
		t.Errorf("Exe = %q, want empty", r.Exe)
	}
}

func TestWithIdentityReplacesDuplicates(t *testing.T) {
	env := []string{"HOME=/wrong", "PATH=/bin", "USER=wrong", "LOGNAME=wrong", "HOME=/again"}
	got := withIdentity(env, "/home/dev", "dev")
	want := []string{"PATH=/bin", "HOME=/home/dev", "USER=dev", "LOGNAME=dev"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRestartOneNoCommand(t *testing.T) {
	err := restartOne(Restartable{Name: "demo"})
	if err == nil || !strings.Contains(err.Error(), "no command line") {
		t.Fatalf("err = %v", err)
	}
}

func TestRestartOneRootUIDZeroStaysRoot(t *testing.T) {
	isolateHome(t)
	old := effectiveUID
	effectiveUID = func() int { return 0 }
	t.Cleanup(func() { effectiveUID = old })

	r := Restartable{Name: "demo", Cmd: []string{"/bin/true"}, Cwd: t.TempDir(), UID: 0}
	if err := restartOne(r); err != nil {
		t.Fatalf("restartOne: %v", err)
	}
}
