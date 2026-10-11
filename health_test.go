package main

import (
	"net"
	"strconv"
	"testing"
)

func TestDialTarget(t *testing.T) {
	cases := map[string]string{
		"0.0.0.0:3000":   "127.0.0.1:3000",
		"*:3000":         "127.0.0.1:3000",
		"[::]:8080":      "127.0.0.1:8080",
		":::8080":        "127.0.0.1:8080",
		"127.0.0.1:3000": "127.0.0.1:3000",
		"[::1]:3000":     "[::1]:3000",
		"::1:3000":       "[::1]:3000",
	}
	for in, want := range cases {
		addr, port := in[:len(in)-5], in[len(in)-4:]
		if got := dialTarget(addr, port); got != want {
			t.Errorf("dialTarget(%q): got %q want %q", in, got, want)
		}
	}
}

func TestCheckHealthUDP(t *testing.T) {
	items := []PortItem{{Proto: "udp", Addr: "0.0.0.0", Port: "53", PID: "1"}}
	res := CheckHealth(items)
	k := healthKey("udp", "0.0.0.0", "53", "1")
	if res[k].status != HealthNotApplicable {
		t.Fatalf("got %+v", res[k])
	}
}

func TestCheckHealthAlive(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no tcp")
	}
	defer l.Close()
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	items := []PortItem{{Proto: "tcp", Addr: "0.0.0.0", Port: port, PID: "1"}}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	res := CheckHealth(items)
	k := healthKey("tcp", "0.0.0.0", port, "1")
	if res[k].status != HealthAlive {
		t.Fatalf("got %+v", res[k])
	}
}

func TestApplyHealth(t *testing.T) {
	m := model{health: map[string]healthResult{
		healthKey("tcp", "127.0.0.1", "3000", "42"): {status: HealthAlive, latency: 5},
		"stale-key": {status: HealthAlive},
	}}
	m.all = []PortItem{
		{Proto: "tcp", Addr: "127.0.0.1", Port: "3000", PID: "42"},
		{Proto: "tcp", Addr: "127.0.0.1", Port: "3001", PID: "43"},
		{Proto: "udp", Addr: "0.0.0.0", Port: "53", PID: "1"},
	}
	m.applyHealth()
	if m.all[0].Health != HealthAlive {
		t.Fatalf("known key: got %v", m.all[0].Health)
	}
	if m.all[1].Health != HealthUnknown {
		t.Fatalf("unknown key: got %v", m.all[1].Health)
	}
	if m.all[2].Health != HealthNotApplicable {
		t.Fatalf("udp: got %v", m.all[2].Health)
	}
}

func TestOrphanItemsDedup(t *testing.T) {
	all := []PortItem{
		{Proto: "tcp", Port: "3000", PID: "42", Process: "node", Orphan: true, Source: "host"},
		{Proto: "udp", Port: "3000", PID: "42", Process: "node", Orphan: true, Source: "host"},
		{Proto: "tcp", Port: "3001", PID: "43", Process: "node", Orphan: true, Source: "host"},
		{Proto: "tcp", Port: "3002", PID: "abc", Process: "docker:web", Orphan: true, Source: "docker"},
		{Proto: "tcp", Port: "3003", PID: "44", Process: "node", Orphan: false, Source: "host"},
	}
	got := orphanItems(all)
	if len(got) != 2 || got[0].PID != "42" || got[1].PID != "43" {
		t.Fatalf("got %+v", got)
	}
}
