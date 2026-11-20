package main

import "testing"

// TestParseSSNamesWithSpaces: comm may contain spaces ("Web Content",
// "tmux: server"); splitting the line on whitespace used to cut the users
// field apart and drop the listener entirely.
func TestParseSSNamesWithSpaces(t *testing.T) {
	out := `Netid State  Recv-Q Send-Q Local Address:Port Peer Address:PortProcess
tcp   LISTEN 0      511    127.0.0.1:3000     0.0.0.0:*    users:(("Web Content",pid=4242,fd=3))
tcp   LISTEN 0      511    127.0.0.1:3001     0.0.0.0:*    users:(("a  b",pid=4243,fd=3),("c (x)",pid=4244,fd=4))
`
	items := parseSS(out)
	if len(items) != 3 {
		t.Fatalf("got %d items: %+v", len(items), items)
	}
	want := []struct{ port, pid, name string }{
		{"3000", "4242", "Web Content"}, {"3001", "4243", "a  b"}, {"3001", "4244", "c (x)"},
	}
	for i, w := range want {
		if items[i].Port != w.port || items[i].PID != w.pid || items[i].Process != w.name {
			t.Errorf("item %d = %+v, want %+v", i, items[i], w)
		}
	}
}

// TestParseSSAddressForms: IPv6, wildcard and %iface-scoped addresses keep
// their host part; a non-numeric port is not a listener we can act on.
func TestParseSSAddressForms(t *testing.T) {
	out := `udp UNCONN 0 0 127.0.0.53%lo:53 0.0.0.0:* users:(("systemd-resolve",pid=500,fd=13))
tcp LISTEN 0 128 [fe80::1%eth0]:22 [::]:* users:(("sshd",pid=600,fd=3))
tcp LISTEN 0 128 *:8080 *:* users:(("caddy",pid=700,fd=7))
tcp LISTEN 0 128 [::]:9090 [::]:* users:(("prom",pid=800,fd=7))
udp UNCONN 0 0 0.0.0.0:* 0.0.0.0:* users:(("weird",pid=900,fd=7))
`
	items := parseSS(out)
	want := []struct{ addr, port string }{
		{"127.0.0.53%lo", "53"}, {"[fe80::1%eth0]", "22"}, {"*", "8080"}, {"[::]", "9090"},
	}
	if len(items) != len(want) {
		t.Fatalf("got %d items: %+v", len(items), items)
	}
	for i, w := range want {
		if items[i].Addr != w.addr || items[i].Port != w.port {
			t.Errorf("item %d = %s %s, want %s %s", i, items[i].Addr, items[i].Port, w.addr, w.port)
		}
	}
}
