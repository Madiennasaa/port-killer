package main

import (
	"strings"
	"testing"
	"time"
)

func TestPad(t *testing.T) {
	if got := pad("ab", 4); got != "ab  " {
		t.Errorf("pad = %q", got)
	}
	if got := pad("abcdef", 4); got != "abc…" {
		t.Errorf("overflow pad = %q", got)
	}
	if got := pad("abc", 1); got != "a" {
		t.Errorf("w<=1 pad = %q", got)
	}
	if got := pad("abc", 0); got != "" {
		t.Errorf("w=0 pad = %q", got)
	}
}

func TestTruncRunes(t *testing.T) {
	if got := truncRunes("abcdef", 3); got != "abc" {
		t.Errorf("trunc = %q", got)
	}
	if got := truncRunes("ab", 5); got != "ab" {
		t.Errorf("short trunc = %q", got)
	}
	if got := truncRunes("abc", 0); got != "abc" {
		t.Errorf("w=0 trunc = %q", got)
	}
}

func TestHealthText(t *testing.T) {
	cases := []struct {
		it   PortItem
		want string
	}{
		{PortItem{Health: HealthAlive, Latency: 500 * time.Microsecond}, "● <1ms"},
		{PortItem{Health: HealthAlive, Latency: 2500 * time.Microsecond}, "● 2ms"},
		{PortItem{Health: HealthDead}, "○ no response"},
		{PortItem{Health: HealthNotApplicable}, "-"},
		{PortItem{Health: HealthUnknown}, "…"},
	}
	for _, c := range cases {
		if got := healthText(c.it); got != c.want {
			t.Errorf("healthText(%+v) = %q, want %q", c.it, got, c.want)
		}
	}
}

func TestRowTextResponsiveColumns(t *testing.T) {
	args := []string{" ", "tcp", "0.0.0.0", "3000", "1234", "1d 02h", "myapp", "● <1ms", "node"}
	narrow := model{width: 60}.rowText(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8])
	mid := model{width: 90}.rowText(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8])
	wide := model{width: 130}.rowText(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8])

	if strings.Contains(narrow, "HEALTH") || strings.Contains(narrow, "myapp") {
		t.Errorf("narrow row must hide optional columns: %q", narrow)
	}
	if !strings.Contains(mid, "● <1ms") {
		t.Errorf("mid row should include health: %q", mid)
	}
	if !strings.Contains(wide, "myapp") || !strings.Contains(wide, "1d 02h") {
		t.Errorf("wide row should include uptime and project: %q", wide)
	}
	if len([]rune(wide)) <= len([]rune(mid)) || len([]rune(mid)) <= len([]rune(narrow)) {
		t.Error("rows should grow with terminal width")
	}
}

func TestViewHistoryEmpty(t *testing.T) {
	m := testModel()
	m.showHistory = true
	out := m.View()
	if !strings.Contains(out, "PORT HISTORY") {
		t.Errorf("missing history header: %q", out)
	}
	if !strings.Contains(out, "No events recorded yet.") {
		t.Errorf("expected empty-history hint: %q", out)
	}
}

func TestViewHistoryEvents(t *testing.T) {
	m := testModel()
	m.showHistory = true
	m.events = []Event{{Port: "3000", Process: "node", Cause: "stopped"}}
	out := m.View()
	if !strings.Contains(out, "3000") {
		t.Errorf("event row missing: %q", out)
	}
}

func TestViewHelp(t *testing.T) {
	m := testModel()
	m.showHelp = true
	if out := m.View(); !strings.Contains(out, "HELP") {
		t.Errorf("help view missing header: %q", out)
	}

	m.helpOverride = []string{" Conflicts for /tmp", ""}
	m.showHelp = true
	if out := m.View(); !strings.Contains(out, "Conflicts for /tmp") {
		t.Errorf("override not rendered: %q", out)
	}
}

func TestViewTableAndStatus(t *testing.T) {
	m := testModel()
	m.width, m.height = 130, 24
	out := m.View()
	for _, want := range []string{"PROTO", "node", "postgres", "port | orphan:", "filter:"} {
		if !strings.Contains(out, want) {
			t.Errorf("table view missing %q", want)
		}
	}
}

func TestViewEmptyList(t *testing.T) {
	m := testModel()
	m.view = nil
	out := m.View()
	if !strings.Contains(out, "No matching ports") {
		t.Errorf("empty state missing: %q", out)
	}
}

func TestViewConfirmPrompt(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.cursor = 0
	m.askKill(false)
	out := m.View()
	if !strings.Contains(out, "[y/N]") {
		t.Errorf("confirm prompt missing: %q", out)
	}
	if !strings.Contains(out, "Stop node") {
		t.Errorf("target missing from prompt: %q", out)
	}

	m = testModel()
	m.askKill(true)
	if out = m.View(); !strings.Contains(out, "KILL -9") {
		t.Errorf("force prompt missing: %q", out)
	}
}

func TestViewFilterPrompt(t *testing.T) {
	m := testModel()
	m.mode = modeFilter
	m.filter = "nod"
	out := m.View()
	if !strings.Contains(out, "Search: nod") {
		t.Errorf("filter prompt missing: %q", out)
	}
}

func TestViewMessageError(t *testing.T) {
	m := testModel()
	m.message, m.isErr = "boom", true
	if out := m.View(); !strings.Contains(out, "boom") {
		t.Errorf("message missing: %q", out)
	}
}

func TestViewDetailPanel(t *testing.T) {
	m := testModel()
	m.width, m.height = 130, 30
	m.showDetail = true
	m.refreshDetail()
	out := m.View()
	if !strings.Contains(out, "PROCESS") || !strings.Contains(out, "node") {
		t.Errorf("detail panel missing content: %q", out)
	}
}

func TestViewOrphanHiddenFlag(t *testing.T) {
	m := testModel()
	m.all = append(m.all, PortItem{Proto: "tcp", Port: "9999", PID: "9", Process: "x", Orphan: true})
	m.rebuild()
	m.filter = "node"
	m.rebuild()
	out := m.View()
	if !strings.Contains(out, "orphans hidden by filter") {
		t.Errorf("orphan flag missing: %q", out)
	}
}

func TestSysUptime(t *testing.T) {
	got := sysUptime()
	if got == "" {
		t.Fatal("sysUptime returned empty")
	}
	if got != "?" && !strings.Contains(got, "d ") {
		t.Errorf("unexpected format %q", got)
	}
}
