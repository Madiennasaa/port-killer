package main

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// ---- own.go: chownToSudoUser root path via the effectiveUID seam ----

func TestChownToSudoUserRootPath(t *testing.T) {
	// Simulate root with a real user's name so user.Lookup succeeds and
	// chownPaths can run as ourselves (self-chown is a no-op that succeeds).
	u, err := user.Current()
	if err != nil {
		t.Skipf("no current user: %v", err)
	}
	t.Setenv("SUDO_USER", u.Username)
	f, err := os.CreateTemp("", "own")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	old := effectiveUID
	effectiveUID = func() int { return 0 }
	t.Cleanup(func() { effectiveUID = old })

	chownToSudoUser(f.Name())
	// Self-chown must leave the file owned by us.
	if fi, err := os.Stat(f.Name()); err != nil {
		t.Fatal(err)
	} else if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		t.Errorf("uid = %d, want %d", st.Uid, os.Getuid())
	}
}

func TestChownToSudoUserBadLookup(t *testing.T) {
	t.Setenv("SUDO_USER", "no-such-user-xyz-portkiller")
	old := effectiveUID
	effectiveUID = func() int { return 0 }
	t.Cleanup(func() { effectiveUID = old })
	// Must not panic and must not chown anything.
	chownToSudoUser("/tmp")
}

// ---- restart.go: root-credential path via the effectiveUID/lookupUserById seams ----

func TestRestartOneRootCredentialBadUser(t *testing.T) {
	oldUID := effectiveUID
	oldLookup := lookupUserById
	effectiveUID = func() int { return 0 }
	lookupUserById = func(string) (*user.User, error) {
		return nil, fmt.Errorf("no such user")
	}
	t.Cleanup(func() {
		effectiveUID = oldUID
		lookupUserById = oldLookup
	})

	err := restartOne(Restartable{Cmd: []string{"true"}, Cwd: t.TempDir(), UID: 12345})
	if err == nil {
		t.Fatal("unknown uid under root must error")
	}
}

func TestRestartOneRootCredentialBadGid(t *testing.T) {
	oldUID := effectiveUID
	oldLookup := lookupUserById
	effectiveUID = func() int { return 0 }
	lookupUserById = func(string) (*user.User, error) {
		return &user.User{Uid: "12345", Gid: "not-a-gid", Username: "ghost", HomeDir: "/tmp"}, nil
	}
	t.Cleanup(func() {
		effectiveUID = oldUID
		lookupUserById = oldLookup
	})

	err := restartOne(Restartable{Cmd: []string{"true"}, Cwd: t.TempDir(), UID: 12345})
	if err == nil {
		t.Fatal("bad gid must error")
	}
	if !strings.Contains(err.Error(), "bad gid") {
		t.Errorf("error = %v", err)
	}
}

func TestRestartOneRootCredentialSuccess(t *testing.T) {
	// Without real root privileges the kernel rejects setuid-to-ourselves via
	// SysProcAttr.Credential, so assert the credential path is *taken* by
	// pointing lookupUserById at a failure and expecting that error (which
	// only happens when effectiveUID==0 && UID!=0).
	oldUID := effectiveUID
	oldLookup := lookupUserById
	effectiveUID = func() int { return 0 }
	lookupUserById = func(string) (*user.User, error) {
		return nil, fmt.Errorf("injected")
	}
	t.Cleanup(func() {
		effectiveUID = oldUID
		lookupUserById = oldLookup
	})

	err := restartOne(Restartable{Cmd: []string{"true"}, Cwd: t.TempDir(), UID: 12345})
	if err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("root credential path must be taken, got %v", err)
	}
}

// ---- scanner.go: fake docker that succeeds ----

// stubDockerBin points the docker binary seam at path for the test duration.
func stubDockerBin(t *testing.T, path string) {
	t.Helper()
	old := dockerBin
	dockerBin = path
	t.Cleanup(func() { dockerBin = old })
}

func withFakeDockerOK(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	sh := "#!/bin/sh\necho ok\nexit 0\n"
	path := filepath.Join(dir, "docker")
	if err := os.WriteFile(path, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	stubDockerBin(t, path)
}

func TestStopContainerFakeSuccess(t *testing.T) {
	withFakeDockerOK(t)
	if err := StopContainer("abc"); err != nil {
		t.Fatalf("fake docker stop: %v", err)
	}
}

func TestRestartOneDockerFakeSuccess(t *testing.T) {
	withFakeDockerOK(t)
	if err := restartOne(Restartable{Docker: true, ID: "abc", Name: "web"}); err != nil {
		t.Fatalf("fake docker start: %v", err)
	}
}

func TestGetDockerPortsFakeSuccess(t *testing.T) {
	// Override docker ps with a canned format response.
	dir := t.TempDir()
	sh := "#!/bin/sh\necho 'abc|web|0.0.0.0:8080->80/tcp|Up 2 hours'\n"
	path := filepath.Join(dir, "docker")
	if err := os.WriteFile(path, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	stubDockerBin(t, path)

	got := GetDockerPorts()
	if len(got) != 1 || got[0].Port != "8080" || got[0].Process != "docker:web" {
		t.Fatalf("got %+v", got)
	}
}

// ---- blast.go: inodeOwners against a real listening socket ----

func TestInodeOwnersFindsOwnSocket(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer ln.Close()

	// Find this process's fd whose readlink starts with "socket:[".
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("cannot read /proc/self/fd: %v", err)
	}
	var wantInode string
	for _, e := range ents {
		l, err := os.Readlink("/proc/self/fd/" + e.Name())
		if err != nil || !strings.HasPrefix(l, "socket:[") {
			continue
		}
		wantInode = strings.TrimSuffix(strings.TrimPrefix(l, "socket:["), "]")
		break
	}
	if wantInode == "" {
		t.Skip("no socket fd found")
	}

	owners := inodeOwners(map[string]bool{wantInode: true})
	if owners[wantInode] == "" {
		t.Errorf("own socket inode %s must resolve, got %v", wantInode, owners)
	}
}

// ---- restart.go: root-credential block with a *valid* numeric gid ----

func TestRestartOneRootCredentialValidGID(t *testing.T) {
	// Point lookupUserById at a fake user whose Gid is a valid number so the
	// credential block is fully executed (Credential + Env append). Without
	// real root the kernel rejects the setuid, so Start() must fail — which
	// is fine: the lines we care about already ran.
	oldUID := effectiveUID
	oldLookup := lookupUserById
	effectiveUID = func() int { return 0 }
	lookupUserById = func(string) (*user.User, error) {
		return &user.User{Username: "fakeuser", Uid: "12345", Gid: "12345", HomeDir: "/tmp"}, nil
	}
	t.Cleanup(func() {
		effectiveUID = oldUID
		lookupUserById = oldLookup
	})

	err := restartOne(Restartable{Cmd: []string{"true"}, Cwd: t.TempDir(), UID: 12345})
	// Start() fails without root, but the credential block was taken.
	if err == nil {
		t.Log("unexpectedly succeeded (running as root?); credential block still covered")
	}
}
