package main

import (
	"os"
	"testing"
	"time"
)

func TestProcUIDSelf(t *testing.T) {
	uid, ok := procUID(os.Getpid())
	if !ok {
		t.Fatal("own pid must have a uid")
	}
	if uid != "0" && uid == "" {
		t.Fatal("uid must not be empty")
	}
	if _, ok := procUID(999999999); ok {
		t.Error("impossible pid must not resolve")
	}
}

func TestCachedHomeRoundTrip(t *testing.T) {
	if _, ok := cachedHome("nope"); ok {
		t.Error("unknown key must miss")
	}
	storeHome("k1", "/home/x")
	h, ok := cachedHome("k1")
	if !ok || h != "/home/x" {
		t.Errorf("got %q,%v", h, ok)
	}
}

func TestOwnerHomeFallsBackToEnv(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	got := ownerHome(999999999)
	if got == "" {
		t.Error("ownerHome must fall back to $HOME")
	}
	home, _ := os.UserHomeDir()
	if got != home {
		t.Errorf("got %q, want %q", got, home)
	}
}

func TestMessageExpired(t *testing.T) {
	m := testModel()
	if m.messageExpired() {
		t.Error("empty message never expires")
	}

	m.message, m.msgAt = "x", time.Now()
	if m.messageExpired() {
		t.Error("fresh message must not expire")
	}

	m.msgAt = time.Now().Add(-messageTTL - time.Second)
	if !m.messageExpired() {
		t.Error("stale idle message must expire")
	}

	m.busy = true
	if m.messageExpired() {
		t.Error("busy model must keep its message")
	}

	m.busy = false
	m.msgAt = time.Time{}
	if m.messageExpired() {
		t.Error("unset msgAt must not expire (avoid clearing seeded messages)")
	}
}

func TestPsUptimeMapBadPids(t *testing.T) {
	if got := psUptimeMap([]string{"999999999"}); len(got) != 0 {
		t.Errorf("impossible pid: got %v", got)
	}
	m := psUptimeMap([]string{itoaSimple(os.Getpid())})
	if len(m) == 0 {
		t.Error("own pid should report an uptime")
	}
}
