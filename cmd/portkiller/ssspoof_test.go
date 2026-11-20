package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeComm serves /proc/<pid>/comm from a map.
func fakeComm(m map[string]string) func(string) (string, bool) {
	return func(pid string) (string, bool) {
		c, ok := m[pid]
		return c, ok
	}
}

// TestVerifyOwnersRejectsSpoofedPID: ss prints comm unescaped, so a local
// user can name a process `x",pid=812,fd=` and make the naive parse report
// pid 812 (any victim) as the owner of their socket. A root portkiller would
// then stop the victim. Owners are checked against /proc/<pid>/comm and the
// real owner is recovered.
func TestVerifyOwnersRejectsSpoofedPID(t *testing.T) {
	out := `tcp LISTEN 0 128 127.0.0.1:47812 0.0.0.0:* users:(("x",pid=812,fd=9",pid=4242,fd=3))
tcp LISTEN 0 128 127.0.0.1:3000 0.0.0.0:* users:(("node",pid=5000,fd=20),("node",pid=5001,fd=20))
tcp LISTEN 0 128 127.0.0.1:3001 0.0.0.0:* users:(("a b",pid=5002,fd=3))
tcp LISTEN 0 128 127.0.0.1:3002 0.0.0.0:* users:(("gone",pid=5003,fd=3))
tcp LISTEN 0 128 127.0.0.1:3003 0.0.0.0:* users:(("liar",pid=5004,fd=3))
`
	comm := fakeComm(map[string]string{
		"812":  "sshd",
		"4242": `x",pid=812,fd=9`,
		"5000": "node", "5001": "node",
		"5002": "a b) tail", // ss cuts the name at the first ")"
		"5004": "honest",
	})
	got := verifyOwners(parseSS(out), comm)
	want := []struct{ port, pid, name string }{
		{"47812", "4242", `x",pid=812,fd=9`},
		{"3000", "5000", "node"}, {"3000", "5001", "node"},
		{"3001", "5002", "a b"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d items: %+v", len(got), got)
	}
	for i, w := range want {
		if got[i].Port != w.port || got[i].PID != w.pid || got[i].Process != w.name {
			t.Errorf("item %d = %s/%s/%q, want %+v", i, got[i].Port, got[i].PID, got[i].Process, w)
		}
	}
}

// TestVerifyOwnersKeepsContainersAndDropsProxyImpostor: container rows have
// no /proc entry and pass through; a verified docker-proxy is still dropped.
func TestVerifyOwnersKeepsContainersAndDropsProxyImpostor(t *testing.T) {
	items := []PortItem{
		{Port: "80", PID: "abc", Process: "docker:web", Source: "docker"},
		{Port: "81", PID: "77", Process: "", Source: "host", ssUsers: `users:(("docker-proxy",pid=77,fd=3))`},
	}
	got := verifyOwners(items, fakeComm(map[string]string{"77": "docker-proxy"}))
	if len(got) != 1 || got[0].Source != "docker" {
		t.Fatalf("got %+v", got)
	}
}

// TestVerifyOwnersRealSpoofedProcess runs the real ss against a process this
// test starts on a free high port with a spoofing comm, and checks the real
// /proc lookup attributes the socket to it, not to pid 1.
func TestVerifyOwnersRealSpoofedProcess(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	if _, err := os.Stat(ssBin); err != nil {
		t.Skip("ss not available")
	}
	port := 0
	for p := 47900; p < 47950; p++ {
		if portFree(p) {
			port = p
			break
		}
	}
	if port == 0 {
		t.Skip("no free port")
	}
	script := `import ctypes,socket,sys,time
ctypes.CDLL(None).prctl(15, b'x",pid=1,fd=9', 0, 0, 0)
s=socket.socket(); s.bind(("127.0.0.1", int(sys.argv[1]))); s.listen()
print("ready", flush=True); time.sleep(20)`
	c := exec.Command(py, "-c", script, strconv.Itoa(port))
	stdout, _ := c.StdoutPipe()
	if err := c.Start(); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	buf := make([]byte, 6)
	if _, err := stdout.Read(buf); err != nil {
		t.Skip("child did not start")
	}
	var mine []PortItem
	for i := 0; i < 20 && len(mine) == 0; i++ {
		out, err := commandOutput(ssBin, "-tulnp")
		if err != nil {
			t.Skip(err)
		}
		for _, it := range verifyOwners(parseSS(string(out)), procComm) {
			if it.Port == strconv.Itoa(port) {
				mine = append(mine, it)
			}
		}
		if len(mine) == 0 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if len(mine) != 1 || mine[0].PID != strconv.Itoa(c.Process.Pid) || !strings.HasPrefix(mine[0].Process, `x"`) {
		t.Fatalf("port %d attributed to %+v, want pid %d", port, mine, c.Process.Pid)
	}
}

// TestProcCommGone: an exited pid has no comm.
func TestProcCommGone(t *testing.T) {
	if _, ok := procComm("999999999"); ok {
		t.Fatal("nonexistent pid has a comm")
	}
	if c, ok := procComm(strconv.Itoa(os.Getpid())); !ok || c == "" {
		t.Fatalf("own comm = %q, %v", c, ok)
	}
}
