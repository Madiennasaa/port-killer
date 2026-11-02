package main

import (
	"testing"
)

func TestParseGuard(t *testing.T) {
	r := parseGuard("# komentar\n5432=postgres-dev\n\n8000 = simas\nrusak\n=x\n9=\n")
	if len(r) != 2 || r[0].Port != "5432" || r[0].Owner != "postgres-dev" || r[1].Port != "8000" || r[1].Owner != "simas" {
		t.Fatalf("got %+v", r)
	}
}

func TestOwnerMatches(t *testing.T) {
	if !ownerMatches(PortItem{Process: "docker:postgres-dev"}, "postgres-dev") {
		t.Fatal("docker name")
	}
	if !ownerMatches(PortItem{Process: "php", Project: "Simas"}, "simas") {
		t.Fatal("project")
	}
	if ownerMatches(PortItem{Process: "node", Project: "x"}, "postgres-dev") {
		t.Fatal("harus tidak cocok")
	}
}

func TestCheckGuard(t *testing.T) {
	rules := []Rule{{Port: "5432", Owner: "postgres-dev"}}
	ok := []PortItem{{Port: "5432", Process: "docker:postgres-dev"}}
	bad := []PortItem{{Port: "5432", Process: "postgres", PID: "9"}, {Port: "80", Process: "nginx"}}
	if len(checkGuard(rules, ok)) != 0 {
		t.Fatal("peowned by sah")
	}
	v := checkGuard(rules, bad)
	if len(v) != 1 || v[0].Item.PID != "9" {
		t.Fatalf("got %+v", v)
	}
	if violationText(v) == "" || violationText(nil) != "" {
		t.Fatal("teks")
	}
}

// TestGuardNotifyText checks the desktop-notification body: no leading space
// and a usable summary of the first violation.
func TestGuardNotifyText(t *testing.T) {
	if got := guardNotifyText(nil); got != "" {
		t.Errorf("guardNotifyText(nil) = %q, want empty", got)
	}
	v := checkGuard([]Rule{{Port: "5432", Owner: "pg"}}, []PortItem{{Port: "5432", Process: "redis", PID: "9"}})
	got := guardNotifyText(v)
	if got != violationText(v)[1:] {
		t.Errorf("guardNotifyText = %q", got)
	}
}

// TestCheckGuardNotifiesOnNewViolation: a first-of-its-kind violation fires the
// notify seam and raises the pending-bell flag; a repeat of the same
// violation must not notify again.
func TestCheckGuardNotifiesOnNewViolation(t *testing.T) {
	old := notifySend
	var calls []string
	notifySend = func(title, body string) error {
		calls = append(calls, title+"|"+body)
		return nil
	}
	t.Cleanup(func() { notifySend = old })

	m := model{
		guard:     []Rule{{Port: "5432", Owner: "pg"}},
		guardSeen: map[string]bool{},
		all:       []PortItem{{Proto: "tcp", Port: "5432", PID: "9", Process: "redis"}},
	}
	m.checkGuardNow()
	if len(calls) != 1 {
		t.Fatalf("notify calls = %d, want 1 (%v)", len(calls), calls)
	}
	if !m.pendingBell {
		t.Error("pendingBell not raised on a new violation")
	}
	// Same violation on the next tick: deduped and no repeat notification.
	m.checkGuardNow()
	if len(calls) != 1 {
		t.Errorf("notify calls after repeat = %d, want 1", len(calls))
	}
	// Violation cleared: a later reappear notifies again.
	m.all = nil
	m.checkGuardNow()
	m.all = []PortItem{{Proto: "tcp", Port: "5432", PID: "9", Process: "redis"}}
	m.checkGuardNow()
	if len(calls) != 2 {
		t.Errorf("notify calls after reappear = %d, want 2", len(calls))
	}
}

func TestCheckGuardNoRulesStaysSilent(t *testing.T) {
	old := notifySend
	var called bool
	notifySend = func(string, string) error { called = true; return nil }
	t.Cleanup(func() { notifySend = old })
	m := model{all: []PortItem{{Port: "5432", PID: "1", Process: "x"}}}
	m.checkGuardNow()
	if called || m.pendingBell {
		t.Error("no rules: must stay silent")
	}
}

// TestPortsMsgConsumesPendingBell: Update wraps the tick command with tea.Bell
// exactly once, then clears the flag.
func TestPortsMsgConsumesPendingBell(t *testing.T) {
	m := model{pendingBell: true, health: map[string]healthResult{}}
	nm, cmd := m.update(portsMsg{Items: []PortItem{}})
	nm2 := nm.(model)
	if nm2.pendingBell {
		t.Error("pendingBell not consumed")
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want health scan plus bell wrapper")
	}
}
