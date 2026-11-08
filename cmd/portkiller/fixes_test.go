package main

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// A server whose cwd cannot be read (deleted directory, permissions) must
// still get its start time, or stopItem skips the recycled-PID check.
func TestEnrichProjectStartTimeWithoutCwd(t *testing.T) {
	const pid = "999999999" // no such process: readCwd fails
	stat := pid + " (node) S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 4242 0 0"
	stubProcTree(t, map[string]string{"/proc/" + pid + "/stat": stat})
	it := PortItem{PID: pid, Process: "node"}
	enrichProject(&it)
	if it.Cwd != "-" {
		t.Fatalf("cwd = %q, want -", it.Cwd)
	}
	if it.startTime != 4242 {
		t.Fatalf("startTime = %d, want 4242", it.startTime)
	}
}

func TestSplitSupervised(t *testing.T) {
	rs := []Restartable{{Name: "vite"}, {Name: "api", Supervisor: "pm2"}}
	run, skipped := splitSupervised(rs)
	if len(run) != 1 || run[0].Name != "vite" {
		t.Fatalf("run = %+v", run)
	}
	if !reflect.DeepEqual(skipped, []string{"api (pm2)"}) {
		t.Fatalf("skipped = %v", skipped)
	}
}

// R must never relaunch a process its manager will respawn.
func TestRestartKeySkipsSupervised(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.restarts = []Restartable{{Name: "api", Supervisor: "pm2", Cmd: []string{"true"}, Cwd: t.TempDir()}}
	nm, cmd, ok := m.restartKey("R")
	if !ok || cmd != nil || nm.busy || !nm.isErr || !strings.Contains(nm.message, "api (pm2)") {
		t.Fatalf("only-supervised: ok=%v cmd=%v busy=%v msg=%q", ok, cmd != nil, nm.busy, nm.message)
	}

	m.restarts = append(m.restarts, Restartable{Name: "vite", Cmd: []string{"true"}, Cwd: t.TempDir(), UID: os.Getuid()})
	nm, cmd, _ = m.restartKey("R")
	if cmd == nil || !nm.busy || !strings.Contains(nm.message, "skipped api (pm2)") {
		t.Fatalf("mixed: cmd=%v busy=%v msg=%q", cmd != nil, nm.busy, nm.message)
	}
}

// The supervisor is recorded at capture time, while the ancestry still exists.
func TestCaptureRestartRecordsSupervisor(t *testing.T) {
	me := strconv.Itoa(os.Getpid())
	stubProcTree(t, map[string]string{"/proc/" + me + "/environ": "pm_id=3\x00"})
	r, ok := captureRestart(PortItem{PID: me, Process: "app", Port: "3000"})
	if !ok || r.Supervisor != "pm2" {
		t.Fatalf("ok=%v supervisor=%q", ok, r.Supervisor)
	}
}

func TestCredentialGroups(t *testing.T) {
	got := credentialGroups([]string{"1000", "998", "bad", "27"}, 1000)
	if !reflect.DeepEqual(got, []uint32{1000, 998, 27}) {
		t.Fatalf("groups = %v", got)
	}
	if got := credentialGroups(nil, 5); !reflect.DeepEqual(got, []uint32{5}) {
		t.Fatalf("no groups = %v", got)
	}
}

func TestApplyRestartCredentialKeepsGroups(t *testing.T) {
	oldUID, oldLookup, oldGroups := effectiveUID, lookupUserById, userGroupIds
	t.Cleanup(func() { effectiveUID, lookupUserById, userGroupIds = oldUID, oldLookup, oldGroups })
	effectiveUID = func() int { return 0 }
	lookupUserById = func(string) (*user.User, error) {
		return &user.User{Uid: "1000", Gid: "1000", Username: "dev", HomeDir: "/home/dev"}, nil
	}
	userGroupIds = func(*user.User) ([]string, error) { return []string{"1000", "998"}, nil }

	c := exec.Command("true")
	c.SysProcAttr = &syscall.SysProcAttr{}
	if err := applyRestartCredential(c, Restartable{UID: 1000}); err != nil {
		t.Fatal(err)
	}
	cr := c.SysProcAttr.Credential
	if cr.Uid != 1000 || cr.Gid != 1000 || !reflect.DeepEqual(cr.Groups, []uint32{1000, 998}) {
		t.Fatalf("credential = %+v", cr)
	}

	// A group lookup failure still keeps the primary group.
	userGroupIds = func(*user.User) ([]string, error) { return nil, errors.New("nss down") }
	c.SysProcAttr = &syscall.SysProcAttr{}
	if err := applyRestartCredential(c, Restartable{UID: 1000}); err != nil {
		t.Fatal(err)
	}
	if g := c.SysProcAttr.Credential.Groups; !reflect.DeepEqual(g, []uint32{1000}) {
		t.Fatalf("fallback groups = %v", g)
	}
}

// Writes into the user's state directory must not follow a planted symlink.
func TestStateWritesRefuseSymlinks(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}

	if _, err := defaultOpenRestartLog(link); err == nil {
		t.Error("restart log must refuse a symlink")
	}
	if err := writeFileNoFollow(link, []byte("x"), 0o644); err == nil {
		t.Error("writeFileNoFollow must refuse a symlink")
	}
	if err := saveHidden(link, map[string]bool{"x": true}); err == nil {
		t.Error("saveHidden must refuse a symlink")
	}
	appendEvents(link, []Event{{Kind: "appeared", Port: "1"}})

	if b, _ := os.ReadFile(victim); string(b) != "secret" {
		t.Fatalf("victim was overwritten: %q", b)
	}
}

func TestWriteFileNoFollow(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := writeFileNoFollow(p, []byte("one two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileNoFollow(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "x" {
		t.Fatalf("content = %q, want truncated rewrite", b)
	}
	if err := writeFileNoFollow(filepath.Join(p, "sub"), nil, 0o644); err == nil {
		t.Error("a path under a file must fail")
	}
}

// chownPaths must change a symlink itself, never its target.
func TestChownPathsDoesNotFollowSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink("/nonexistent-portkiller-target", link); err != nil {
		t.Fatal(err)
	}
	// Chown to our own ids is permitted without root; on a dangling link it
	// only succeeds if the link itself is chowned.
	if err := chownPaths([]string{link, target}, os.Getuid(), os.Getgid()); err != nil {
		t.Fatalf("chownPaths: %v", err)
	}
}
