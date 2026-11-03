package main

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// writeShim writes an executable sh script named name and returns its path.
func writeShim(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// stubContainerEnv points every container-related seam at test values for
// the duration of t.
func stubContainerEnv(t *testing.T, docker, podman, runuser string, euid int, sudoUser string) {
	t.Helper()
	oldD, oldP, oldR, oldE := dockerBin, podmanBin, runuserBin, envBin
	oldUID, oldLookup := effectiveUID, lookupUser
	t.Cleanup(func() {
		dockerBin, podmanBin, runuserBin, envBin = oldD, oldP, oldR, oldE
		effectiveUID, lookupUser = oldUID, oldLookup
	})
	dockerBin, podmanBin, runuserBin, envBin = docker, podman, runuser, "/usr/bin/env"
	effectiveUID = func() int { return euid }
	lookupUser = func(name string) (*user.User, error) {
		if name == "ghost" {
			return nil, errors.New("no such user")
		}
		return &user.User{Username: name, Uid: "1000", Gid: "1000", HomeDir: "/home/" + name}, nil
	}
	t.Setenv("SUDO_USER", sudoUser)
}

func TestContainerName(t *testing.T) {
	for in, want := range map[string]string{"docker:web": "web", "podman:db": "db", "node": "node"} {
		if got := containerName(PortItem{Process: in}); got != want {
			t.Errorf("containerName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRuntimeOf(t *testing.T) {
	if runtimeOf("podman") != "podman" || runtimeOf("docker") != "docker" || runtimeOf("") != "docker" {
		t.Error("runtimeOf must map podman to podman and everything else to docker")
	}
}

func TestIsContainer(t *testing.T) {
	if !(PortItem{Source: "podman"}).IsContainer() || !(PortItem{Source: "docker"}).IsContainer() || (PortItem{Source: "host"}).IsContainer() {
		t.Error("IsContainer must cover docker and podman only")
	}
}

func TestPodmanUsers(t *testing.T) {
	stubContainerEnv(t, "", "", "", 1000, "alice")
	if got := podmanUsers(); len(got) != 1 || got[0] != "" {
		t.Errorf("non-root: %q", got)
	}
	effectiveUID = func() int { return 0 }
	if got := podmanUsers(); len(got) != 2 || got[1] != "alice" {
		t.Errorf("root via sudo: %q", got)
	}
	t.Setenv("SUDO_USER", "root")
	if got := podmanUsers(); len(got) != 1 {
		t.Errorf("sudo from root must not scan root twice: %q", got)
	}
}

func TestContainerArgv(t *testing.T) {
	stubContainerEnv(t, "/bin/docker", "/bin/podman", "/bin/runuser", 0, "")
	if bin, argv, _ := containerArgv("docker", "", "stop", "x"); bin != "/bin/docker" || strings.Join(argv, " ") != "stop x" {
		t.Errorf("docker: %s %q", bin, argv)
	}
	bin, argv, err := containerArgv("podman", "alice", "stop", "x")
	want := "-u alice -- /usr/bin/env HOME=/home/alice XDG_RUNTIME_DIR=/run/user/1000 /bin/podman stop x"
	if err != nil || bin != "/bin/runuser" || strings.Join(argv, " ") != want {
		t.Errorf("rootless: %s %q %v", bin, argv, err)
	}
	if _, _, err := containerArgv("podman", "ghost", "ps"); err == nil {
		t.Error("unknown user must fail")
	}
}

func TestContainerCmd(t *testing.T) {
	ok := writeShim(t, "podman", "exit 0\n")
	stubContainerEnv(t, "", ok, "", 0, "")
	if err := containerCmd("podman", "", "stop", "x"); err != nil {
		t.Errorf("success: %v", err)
	}
	podmanBin = writeShim(t, "podman", "echo no such container >&2\nexit 125\n")
	if err := containerCmd("podman", "", "stop", "x"); err == nil || !strings.Contains(err.Error(), "no such container") {
		t.Errorf("failure must carry stderr: %v", err)
	}
	if err := containerCmd("podman", "ghost", "stop", "x"); err == nil {
		t.Error("argv error must surface")
	}
}

func TestGetPodmanPortsMissingBinary(t *testing.T) {
	stubContainerEnv(t, "", "podman", "", 0, "")
	if got := GetPodmanPorts(); got != nil {
		t.Errorf("unresolved podman must be skipped: %+v", got)
	}
}

func TestGetPodmanPortsRootAndRootless(t *testing.T) {
	pod := writeShim(t, "podman", "echo 'r1|rootdb|0.0.0.0:5432->5432/tcp|Up 1 hour'\n")
	// runuser stands in for the rootless account: it prints that user's view.
	ru := writeShim(t, "runuser", "echo 'u1|web|0.0.0.0:8080->80/tcp|Up 5 minutes'\n")
	stubContainerEnv(t, "", pod, ru, 0, "alice")
	got := GetPodmanPorts()
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Process != "podman:rootdb" || got[0].Source != "podman" || got[0].ctrUser != "" {
		t.Errorf("root container: %+v", got[0])
	}
	if got[1].Process != "podman:web" || got[1].ctrUser != "alice" {
		t.Errorf("rootless container: %+v", got[1])
	}
}

func TestGetPodmanPortsSkipsFailures(t *testing.T) {
	pod := writeShim(t, "podman", "exit 1\n")
	stubContainerEnv(t, "", pod, "/bin/runuser", 0, "ghost")
	if got := GetPodmanPorts(); len(got) != 0 {
		t.Errorf("failed scans must yield nothing: %+v", got)
	}
}

func TestContainerPortsDedupesShim(t *testing.T) {
	line := "echo 'abc|web|0.0.0.0:8080->80/tcp|Up 2 hours'\n"
	stubContainerEnv(t, writeShim(t, "docker", line), writeShim(t, "podman", line+"echo 'def|db|0.0.0.0:5432->5432/tcp|Up 1 hour'\n"), "", 1000, "")
	got := containerPorts()
	if len(got) != 2 || got[0].Source != "docker" || got[1].Process != "podman:db" {
		t.Errorf("got %+v", got)
	}
}

func TestDropContainerProxies(t *testing.T) {
	items := []PortItem{
		{Proto: "tcp", Port: "8080", Process: "rootlessport", Source: "host"},
		{Proto: "tcp", Port: "9000", Process: "rootlessport", Source: "host"},
		{Proto: "tcp", Port: "8080", Process: "podman:web", Source: "podman"},
		{Proto: "tcp", Port: "8080", Process: "node", Source: "host"},
	}
	got := dropContainerProxies(items)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	for _, it := range got {
		if it.Process == "rootlessport" && it.Port == "8080" {
			t.Error("proxy for a published port must be dropped")
		}
	}
}

func TestStopAndRestartPodmanRootless(t *testing.T) {
	log := filepath.Join(t.TempDir(), "argv")
	ru := writeShim(t, "runuser", `echo "$@" >> `+log+"\n")
	stubContainerEnv(t, "", "/bin/podman", ru, 0, "")
	it := PortItem{PID: "u1", Process: "podman:web", Source: "podman", ctrUser: "alice"}
	if err := stopItem(it, false); err != nil {
		t.Fatalf("stop: %v", err)
	}
	r, ok := captureRestart(it)
	if !ok || restartDesc(r) != "podman start web" {
		t.Fatalf("capture: %+v %v", r, ok)
	}
	if err := restartOne(r); err != nil {
		t.Fatalf("restart: %v", err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "-u alice") || !strings.Contains(got, "/bin/podman stop u1") || !strings.Contains(got, "/bin/podman start u1") {
		t.Errorf("runuser argv:\n%s", got)
	}
}
