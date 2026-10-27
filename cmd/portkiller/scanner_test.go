package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseProcessInfo(t *testing.T) {
	cases := []struct {
		in      string
		wantPID string
		wantPr  string
	}{
		{`users:(("node",pid=1234,fd=10))`, "1234", "node"},
		{`users:(("sshd",pid=9,fd=3))`, "9", "sshd"},
		{`users:(("docker-proxy",pid=77,fd=4))`, "77", "docker-proxy"},
		{`users:(("weird"name",pid=42,fd=4))`, "42", "weird"},
		{`-`, "", "unknown"},
		{`users:(("nonpid",fd=4))`, "", "unknown"},
	}
	for _, c := range cases {
		pid, proc := parseProcessInfo(c.in)
		if pid != c.wantPID || proc != c.wantPr {
			t.Errorf("parseProcessInfo(%q) = %q,%q want %q,%q", c.in, pid, proc, c.wantPID, c.wantPr)
		}
	}
}

func TestParseSS(t *testing.T) {
	out := `
Netid State  Recv-Q Send-Q Local Address:Port Peer Address:PortProcess
tcp   LISTEN 0      4096   127.0.0.1:631     0.0.0.0:*    users:(("cupsd",pid=900,fd=7))
tcp   LISTEN 0      511    0.0.0.0:3000      0.0.0.0:*    users:(("node",pid=101,fd=18))
tcp   LISTEN 0      511    0.0.0.0:3000      0.0.0.0:*    users:(("node",pid=101,fd=19))
udp   UNCONN 0      0      0.0.0.0:5353      0.0.0.0:*    users:(("avahi-daemon",pid=55,fd=12))
tcp   LISTEN 0      128    127.0.0.11:45678  0.0.0.0:*    users:(("docker-proxy",pid=700,fd=4))
tcp   LISTEN 0      128    [::1]:8080        [::]:*       users:(("python3",pid=300,fd=5))
tcp   ESTAB  0      0      192.168.1.5:22    192.168.1.9:5000 users:(("sshd",pid=800,fd=3))
tcp   LISTEN 0      128    0.0.0.0:9999      0.0.0.0:*    -
short line
`
	items := parseSS(out)
	want := map[string]PortItem{}
	for _, it := range items {
		want[it.Port] = it
	}
	if len(items) != 4 {
		t.Fatalf("got %d items, want 4: %+v", len(items), items)
	}
	if it := want["3000"]; it.PID != "101" || it.Process != "node" || it.Proto != "tcp" || it.Addr != "0.0.0.0" {
		t.Errorf("3000 = %+v", it)
	}
	if it := want["631"]; it.Process != "cupsd" {
		t.Errorf("631 = %+v", it)
	}
	if it := want["5353"]; it.Proto != "udp" {
		t.Errorf("5353 proto = %q", it.Proto)
	}
	if it := want["8080"]; it.Addr != "[::1]" {
		t.Errorf("8080 addr = %q", it.Addr)
	}
	for _, it := range items {
		if it.Process == "docker-proxy" {
			t.Error("docker-proxy must be dropped (docker source owns it)")
		}
		if it.Port == "9999" {
			t.Error("rows without process info must be dropped")
		}
	}
}

func TestParseSSEmpty(t *testing.T) {
	if got := parseSS(""); len(got) != 0 {
		t.Errorf("empty output: got %d items", len(got))
	}
	if got := parseSS("Netid State  Recv-Q Send-Q Local Address:Port Peer Address:PortProcess\n"); len(got) != 0 {
		t.Errorf("header only: got %d items", len(got))
	}
}

func TestParseDockerPS(t *testing.T) {
	out := "abc123|web|0.0.0.0:8080->80/tcp, 127.0.0.1:8443->443/tcp|Up 2 hours\n" +
		"def445|db|0.0.0.0:5432->5432/tcp|Up 5 days\n" +
		"ghi789|noports||Up 1 minute\n" +
		"badline|missing-fields\n"
	items := parseDockerPS(out)
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3: %+v", len(items), items)
	}
	byPort := map[string]PortItem{}
	for _, it := range items {
		byPort[it.Port] = it
	}
	if it := byPort["8080"]; it.Process != "docker:web" || it.Source != "docker" || it.Uptime != "2 hours" {
		t.Errorf("8080 = %+v", it)
	}
	if it := byPort["8443"]; it.Proto != "tcp" || it.Addr != "127.0.0.1" {
		t.Errorf("8443 = %+v", it)
	}
	if it := byPort["5432"]; it.PID != "def445" {
		t.Errorf("5432 = %+v", it)
	}
	for _, it := range items {
		if !it.IsDocker() {
			t.Errorf("%+v should be a docker item", it)
		}
	}
}

func TestParseDockerPSEmpty(t *testing.T) {
	if got := parseDockerPS(""); len(got) != 0 {
		t.Errorf("got %d items", len(got))
	}
}

func TestFillUptimeEmpty(t *testing.T) {
	fillUptime(nil)
	fillUptime([]PortItem{{Process: "docker:x", PID: "abc"}})
}

func TestFillUptimeSelf(t *testing.T) {
	items := []PortItem{{Process: "portkiller.test", PID: strconv.Itoa(os.Getpid())}}
	fillUptime(items)
	if items[0].Uptime == "" {
		t.Error("expected uptime for the test process itself")
	}
}

func TestKillProcessBadPID(t *testing.T) {
	if err := KillProcess("not-a-number", false); err == nil {
		t.Error("non-numeric pid must error")
	}
	if err := KillProcess("999999999", true); err == nil {
		t.Error("impossible pid must error")
	}
}

func TestScanPortsRuns(t *testing.T) {
	items, err := scanPorts()
	if err != nil {
		t.Skipf("ss unavailable: %v", err)
	}
	seen := map[string]bool{}
	for _, it := range items {
		key := it.Proto + "|" + it.Port + "|" + it.PID
		if seen[key] && it.Source == "host" {
			t.Errorf("duplicate host item %v", it)
		}
		seen[key] = true
		if it.IsDocker() && it.Orphan {
			t.Error("docker items are never orphans")
		}
	}
}

func TestGetDockerPortsWithoutDocker(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		if got := GetDockerPorts(); got != nil {
			t.Errorf("no docker on PATH: got %+v", got)
		}
	}
}

func TestScanPortsSurfacesSSError(t *testing.T) {
	old := ssBin
	ssBin = filepath.Join(t.TempDir(), "missing-ss")
	t.Cleanup(func() { ssBin = old })

	items, err := scanPorts()
	if err == nil || !strings.Contains(err.Error(), "ss -tulnp") {
		t.Fatalf("err = %v, want an ss failure", err)
	}
	if items == nil {
		t.Error("items must be non-nil even when ss fails")
	}
}

func TestInitialModelShowsScanError(t *testing.T) {
	withTempHome(t)
	old := ssBin
	ssBin = filepath.Join(t.TempDir(), "missing-ss")
	t.Cleanup(func() { ssBin = old })

	m := initialModel()
	if !m.isErr || !strings.Contains(m.message, "ss -tulnp") {
		t.Errorf("initial scan error not surfaced: %q (isErr=%v)", m.message, m.isErr)
	}
}

func TestParseSSMultiOwnerSocket(t *testing.T) {
	out := `tcp LISTEN 0 4096 0.0.0.0:3100 0.0.0.0:* users:(("node",pid=111,fd=1),("node",pid=112,fd=2))`
	items := parseSS(out)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(items), items)
	}
	seen := map[string]bool{}
	for _, it := range items {
		seen[it.PID] = true
		if it.Port != "3100" || it.Process != "node" {
			t.Errorf("item = %+v", it)
		}
	}
	if !seen["111"] || !seen["112"] {
		t.Errorf("pids = %v, want 111 and 112", seen)
	}
}

func TestParseProcessInfos(t *testing.T) {
	refs := parseProcessInfos(`users:(("node",pid=111,fd=1),("node",pid=112,fd=2))`)
	if len(refs) != 2 || refs[0] != [2]string{"111", "node"} || refs[1] != [2]string{"112", "node"} {
		t.Errorf("refs = %v", refs)
	}
	for _, bad := range []string{"", "-", `users:(("x`, `users:(("x"`, `users:(("x",fd=1))`, `users:(("x",pid=,fd=1))`} {
		if got := parseProcessInfos(bad); len(got) != 0 {
			t.Errorf("parseProcessInfos(%q) = %v, want empty", bad, got)
		}
	}
}
