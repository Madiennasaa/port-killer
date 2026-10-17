package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// testModel builds a model with a fixed row list and no I/O side effects.
func testModel() model {
	m := model{width: 80, height: 24, health: map[string]healthResult{}, guardSeen: map[string]bool{}}
	m.all = []PortItem{
		{Proto: "tcp", Addr: "0.0.0.0", Port: "3000", PID: "101", Process: "node", Source: "host", Project: "app"},
		{Proto: "tcp", Addr: "0.0.0.0", Port: "5432", PID: "102", Process: "postgres", Source: "host", Project: "db"},
		{Proto: "udp", Addr: "0.0.0.0", Port: "5353", PID: "103", Process: "avahi", Source: "host"},
	}
	m.rebuild()
	return m
}

func key(s string) tea.KeyMsg { return keyFromString(s) }

func TestHandleKeyQuit(t *testing.T) {
	for _, k := range []string{"ctrl+c", "q", "f10"} {
		m := testModel()
		_, cmd := m.handleKey(key(k))
		if cmd == nil {
			t.Errorf("key %q: expected quit command", k)
		}
	}
}

func TestHandleKeyNavigation(t *testing.T) {
	cases := []struct {
		key   string
		want  int
		fromN bool
	}{
		{"j", 1, false},
		{"down", 1, false},
		{"g", 0, true},
		{"home", 0, true},
		{"G", 1, true},
		{"end", 1, true},
		{"k", 0, false},
	}
	for _, c := range cases {
		m := testModel()
		m.cursor = 1
		if c.fromN {
			m.cursor = 2
		}
		nm, cmd := m.handleKey(key(c.key))
		got := nm.(model)
		if got.cursor != c.want {
			t.Errorf("key %q: cursor = %d, want %d", c.key, got.cursor, c.want)
		}
		if cmd != nil {
			t.Errorf("key %q: unexpected command", c.key)
		}
	}
}

func TestHandleKeyNavigationClamps(t *testing.T) {
	m := testModel()
	m.cursor = 0
	nm, _ := m.handleKey(key("up"))
	if nm.(model).cursor != 0 {
		t.Error("cursor should clamp at 0")
	}
	m = testModel()
	m.cursor = len(m.view) - 1
	nm, _ = m.handleKey(key("down"))
	if nm.(model).cursor != len(m.view)-1 {
		t.Errorf("cursor should clamp at last row (%d), got %d", len(m.view)-1, nm.(model).cursor)
	}
}

func TestHandleKeyFilterMode(t *testing.T) {
	m := testModel()
	nm, _ := m.handleKey(key("/"))
	got := nm.(model)
	if got.mode != modeFilter {
		t.Fatalf("mode = %v, want modeFilter", got.mode)
	}

	nm, _ = got.handleKey(key("p"))
	got = nm.(model)
	if got.filter != "p" {
		t.Errorf("filter = %q, want %q", got.filter, "p")
	}

	nm, _ = got.handleKey(key("backspace"))
	got = nm.(model)
	if got.filter != "" {
		t.Errorf("filter after backspace = %q, want empty", got.filter)
	}

	nm, _ = got.handleKey(key("enter"))
	if nm.(model).mode != modeNormal {
		t.Error("enter should leave filter mode")
	}
}

func TestHandleKeyFilterEscClears(t *testing.T) {
	m := testModel()
	m.mode = modeFilter
	m.filter = "abc"
	nm, _ := m.handleKey(key("esc"))
	got := nm.(model)
	if got.mode != modeNormal || got.filter != "" {
		t.Errorf("esc: mode=%v filter=%q, want normal/empty", got.mode, got.filter)
	}
}

func TestHandleKeyFilterIgnoresNonRune(t *testing.T) {
	m := testModel()
	m.mode = modeFilter
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if nm.(model).filter != "" {
		t.Error("arrow keys must not type into the filter")
	}
}

func TestHandleKeyConfirmCancel(t *testing.T) {
	for _, mode := range []mode{modeConfirm, modeConfirmOrphans, modeConfirmProject} {
		m := testModel()
		m.mode = mode
		nm, cmd := m.handleKey(key("n"))
		got := nm.(model)
		if got.mode != modeNormal {
			t.Errorf("mode %v: dialog should close", mode)
		}
		if cmd != nil {
			t.Errorf("mode %v: cancel must not run a command", mode)
		}
		if got.message != "Cancelled." {
			t.Errorf("mode %v: message = %q", mode, got.message)
		}
	}
}

func TestHandleKeyConfirmKill(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.cursor = 0
	m.askKill(false)
	if m.mode != modeConfirm {
		t.Fatalf("askKill should arm modeConfirm, got %v", m.mode)
	}
	nm, cmd := m.handleKey(key("y"))
	got := nm.(model)
	if cmd == nil {
		t.Fatal("confirming a kill must return a command")
	}
	if got.mode != modeNormal || !got.busy {
		t.Errorf("mode=%v busy=%v, want normal/busy", got.mode, got.busy)
	}
	if got.cause != "stopped via portkiller" {
		t.Errorf("cause = %q", got.cause)
	}
}

func TestHandleKeyConfirmIgnoredWhenBusy(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.busy = true
	nm, cmd := m.handleKey(key("enter"))
	if nm.(model).mode == modeConfirm {
		t.Error("busy model must not arm a dialog")
	}
	if cmd != nil {
		t.Error("busy model must not start work")
	}
}

func TestHandleKeyOverlay(t *testing.T) {
	m := testModel()
	m.showHistory = true
	nm, cmd := m.handleKey(key("x"))
	if nm.(model).showHistory {
		t.Error("history overlay should close")
	}
	if cmd != nil {
		t.Error("closing overlay must not quit")
	}

	m = testModel()
	m.showHelp = true
	m.helpOverride = []string{"conflicts"}
	nm, _ = m.handleKey(key("x"))
	got := nm.(model)
	if got.showHelp || got.helpOverride != nil {
		t.Error("help overlay and override should clear")
	}

	m = testModel()
	m.showHelp = true
	_, cmd = m.handleKey(key("ctrl+c"))
	if cmd == nil {
		t.Error("ctrl+c in overlay should quit")
	}
}

func TestHandleKeyToggles(t *testing.T) {
	m := testModel()
	m.view = m.all

	nm, _ := m.handleKey(key("i"))
	if !nm.(model).showDetail {
		t.Error("i should toggle detail")
	}
	nm, _ = nm.(model).handleKey(key("i"))
	if nm.(model).showDetail {
		t.Error("i should toggle detail back off")
	}

	proto := m.proto
	nm, _ = nm.(model).handleKey(key("t"))
	if nm.(model).proto == proto {
		t.Error("t should cycle the protocol filter")
	}

	sortBy := m.sortBy
	nm, _ = nm.(model).handleKey(key("s"))
	if nm.(model).sortBy == sortBy {
		t.Error("s should cycle the sort mode")
	}

	nm, _ = m.handleKey(key("o"))
	if !nm.(model).orphans {
		t.Error("o should enable the orphans filter")
	}
	nm, _ = nm.(model).handleKey(key("esc"))
	got := nm.(model)
	if got.filter != "" {
		t.Errorf("esc should clear the text filter, got %q", got.filter)
	}
	if !got.orphans {
		t.Error("esc must not toggle the orphans filter (that is what o is for)")
	}
}

func TestHandleKeyHelpAndHistory(t *testing.T) {
	m := testModel()
	nm, _ := m.handleKey(key("?"))
	if !nm.(model).showHelp {
		t.Error("? should open help")
	}

	m = testModel()
	nm, _ = m.handleKey(key("h"))
	got := nm.(model)
	if !got.showHistory {
		t.Error("h should open history")
	}
}

func TestHandleKeyRestartEmpty(t *testing.T) {
	m := testModel()
	nm, cmd := m.handleKey(key("R"))
	got := nm.(model)
	if cmd != nil {
		t.Error("nothing to restart: no command expected")
	}
	if got.busy {
		t.Error("nothing to restart: must not go busy")
	}
	if got.message != "No recently stopped process to restart." {
		t.Errorf("message = %q", got.message)
	}
}

func TestHandleKeyRefresh(t *testing.T) {
	m := testModel()
	_, cmd := m.handleKey(key("r"))
	if cmd == nil {
		t.Error("r should trigger a refresh")
	}
}

func TestMouseWheel(t *testing.T) {
	m := testModel()
	m.view = m.all
	got := m.mouseWheel(1)
	if got.cursor != 1 {
		t.Errorf("wheel down: cursor = %d", got.cursor)
	}
	got = m.mouseWheel(-2)
	if got.cursor != 0 {
		t.Errorf("wheel up past top: cursor = %d", got.cursor)
	}
}

func TestFooterClickGap(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.height = 24
	// X far beyond the footer: no key, no mode change.
	nm, cmd, ok := m.footerClick(10000)
	if ok || cmd != nil || nm.mode != modeNormal {
		t.Errorf("gap click: ok=%v cmd=%v mode=%v", ok, cmd, nm.mode)
	}
}

func TestRowClickOutOfRange(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.height = 24
	// Header row (y=0) and a row past the table: no selection change.
	if nm, cmd := m.rowClick(0, false); nm.cursor != 0 || cmd != nil {
		t.Errorf("header click: cursor=%d cmd=%v", nm.cursor, cmd)
	}
	m.view = nil
	if nm, cmd := m.rowClick(1, false); cmd != nil || nm.cursor != 0 {
		t.Errorf("empty table click: cursor=%d cmd=%v", nm.cursor, cmd)
	}
}

func TestRowClickSecondClickConfirms(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.height = 24
	m.cursor = 1 // start away from the row we click
	// First click selects; second click on the same row arms the dialog.
	nm, _ := m.rowClick(1, false)
	if nm.cursor != 0 || nm.mode != modeNormal {
		t.Fatalf("first click should only select row 0, cursor=%d mode=%v", nm.cursor, nm.mode)
	}
	nm, cmd := nm.rowClick(1, false)
	if nm.mode != modeConfirm {
		t.Errorf("second click should confirm, mode = %v", nm.mode)
	}
	if cmd == nil {
		t.Error("confirm click should request blast info")
	}
}
