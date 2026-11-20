package main

import (
	"context"
	"net"
	"syscall"
	"testing"
	"time"
)

// TestDialTargetZonesAndV6Wildcard: an IPv4 address with a device suffix
// (ss prints "127.0.0.53%lo" for SO_BINDTODEVICE sockets) is not a hostname
// to resolve, and ss's "[::]" is an IPv6-only wildcard that 127.0.0.1 can
// never reach ("*" is the dual-stack one).
func TestDialTargetZonesAndV6Wildcard(t *testing.T) {
	cases := map[[2]string]string{
		{"127.0.0.53%lo", "53"}:  "127.0.0.53:53",
		{"0.0.0.0%eth0", "80"}:   "127.0.0.1:80",
		{"*%lo", "80"}:           "127.0.0.1:80",
		{"[::]", "80"}:           "[::1]:80",
		{"::", "80"}:             "[::1]:80",
		{"[fe80::1%eth0]", "80"}: "[fe80::1%eth0]:80",
		{"fe80::1%eth0", "80"}:   "[fe80::1%eth0]:80",
		{"[::]%lo", "80"}:        "[::1]:80",
	}
	for in, want := range cases {
		if got := dialTarget(in[0], in[1]); got != want {
			t.Errorf("dialTarget(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

// TestCheckOneV6OnlyWildcard: a real IPV6_V6ONLY listener on [::] reports
// Alive (it was Dead: the probe went to 127.0.0.1).
func TestCheckOneV6OnlyWildcard(t *testing.T) {
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		_ = c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_V6ONLY, 1)
		})
		return serr
	}}
	l, err := lc.Listen(context.Background(), "tcp6", "[::]:0")
	if err != nil {
		t.Skip("no IPv6:", err)
	}
	defer l.Close()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	if st, _ := checkOne("[::]", port, time.Second); st != HealthAlive {
		t.Fatalf("v6only [::] listener reported %v", st)
	}
}
