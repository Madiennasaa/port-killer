package main

import (
	"testing"
)

func TestProtoMatches(t *testing.T) {
	tcp := PortItem{Proto: "tcp"}
	udp := PortItem{Proto: "udp"}
	udpLite := PortItem{Proto: "udplite"}
	m := testModel()
	cases := []struct {
		proto int
		want  map[PortItem]bool
	}{
		{0, map[PortItem]bool{tcp: true, udp: false, udpLite: false}},
		{1, map[PortItem]bool{tcp: false, udp: true, udpLite: true}},
		{2, map[PortItem]bool{tcp: true, udp: true, udpLite: true}},
	}
	for _, c := range cases {
		m.proto = c.proto
		for it, want := range c.want {
			if got := m.protoMatches(it); got != want {
				t.Errorf("proto=%d item=%q: got %v want %v", c.proto, it.Proto, got, want)
			}
		}
	}
}

func TestMatchesFilter(t *testing.T) {
	m := testModel()
	it := PortItem{Port: "3000", Process: "node", PID: "101", Addr: "0.0.0.0", Proto: "tcp", Project: "app", Cwd: "/srv/app"}

	if !m.matchesFilter(it, "") {
		t.Error("empty filter must match everything")
	}
	if !m.matchesFilter(it, "node") {
		t.Error("process name must match")
	}
	if !m.matchesFilter(it, "app") {
		t.Error("project must match")
	}
	if !m.matchesFilter(it, "/srv/app") {
		t.Error("cwd must match")
	}
	if m.matchesFilter(it, "postgres") {
		t.Error("non-matching filter must reject")
	}

	m.orphans = true
	if m.matchesFilter(it, "") {
		t.Error("orphan-only filter must reject a non-orphan")
	}
	if !m.matchesFilter(PortItem{Orphan: true, Process: "x"}, "") {
		t.Error("orphan-only filter must keep orphans")
	}

	// Cwd of "-" is placeholder noise: never searchable.
	m.orphans = false
	if m.matchesFilter(PortItem{Port: "1", Cwd: "-"}, "-") {
		t.Error("placeholder cwd must not be searchable")
	}
}

func TestSortView(t *testing.T) {
	items := []PortItem{
		{Port: "8080", Process: "nginx", PID: "20", Proto: "tcp"},
		{Port: "3000", Process: "node", PID: "5", Proto: "tcp"},
		{Port: "5432", Process: "postgres", PID: "100", Proto: "tcp"},
	}
	cases := []struct {
		sortBy int
		first  string // Port of the expected first row
	}{
		{0, "3000"}, // numeric port
		{1, "8080"}, // process name
		{2, "3000"}, // numeric pid
		{3, "8080"}, // proto: all tcp here, stable sort keeps input order
	}
	for _, c := range cases {
		got := append([]PortItem(nil), items...)
		sortView(got, c.sortBy)
		if got[0].Port != c.first {
			t.Errorf("sortBy=%d: first = %s, want %s", c.sortBy, got[0].Port, c.first)
		}
	}
}

func TestSplitAddr(t *testing.T) {
	addr, port, ok := splitAddr("127.0.0.1:631")
	if !ok || addr != "127.0.0.1" || port != "631" {
		t.Errorf("got %q,%q,%v", addr, port, ok)
	}
	addr, port, ok = splitAddr("[::1]:8080")
	if !ok || addr != "[::1]" || port != "8080" {
		t.Errorf("got %q,%q,%v", addr, port, ok)
	}
	if _, _, ok := splitAddr("no-port-here"); ok {
		t.Error("missing colon must not be ok")
	}
}

func TestSSListening(t *testing.T) {
	if ssListening([]string{"a"}) {
		t.Error("short line must not be listening")
	}
	if !ssListening([]string{"tcp", "LISTEN", "0", "0", "a:1", "b:*", "users:(())"}) {
		t.Error("LISTEN must be listening")
	}
	if !ssListening([]string{"udp", "UNCONN", "0", "0", "a:1", "b:*", "users:(())"}) {
		t.Error("UNCONN must be listening")
	}
	if ssListening([]string{"tcp", "ESTAB", "0", "0", "a:1", "b:*", "users:(())"}) {
		t.Error("ESTAB must not be listening")
	}
}

func TestPruneStaleHealth(t *testing.T) {
	m := testModel()
	m.health = nil
	m.pruneStaleHealth()
	if m.health != nil {
		t.Error("nil health map must stay nil")
	}

	live := healthKey("tcp", "0.0.0.0", "3000", "101")
	stale := healthKey("tcp", "0.0.0.0", "9999", "404")
	m.health = map[string]healthResult{
		live:  {status: HealthAlive},
		stale: {status: HealthDead},
	}
	m.pruneStaleHealth()
	if _, ok := m.health[stale]; ok {
		t.Error("stale entry must be dropped")
	}
	if _, ok := m.health[live]; !ok {
		t.Error("live entry must survive")
	}
}

func TestFinishWork(t *testing.T) {
	m := testModel()
	m.busy = true
	nm, cmd := m.finishWork("ok-msg", "err-msg", nil)
	if nm.busy || nm.message != "ok-msg" || nm.isErr || cmd == nil {
		t.Errorf("ok path: busy=%v msg=%q err=%v cmd=%v", nm.busy, nm.message, nm.isErr, cmd)
	}

	m = testModel()
	m.busy = true
	nm, _ = m.finishWork("ok-msg", "err-msg", errTest)
	if nm.busy || nm.message != "err-msg" || !nm.isErr {
		t.Errorf("err path: busy=%v msg=%q err=%v", nm.busy, nm.message, nm.isErr)
	}
	if nm.msgAt.IsZero() {
		t.Error("finishWork must stamp msgAt so the message expires")
	}
}
