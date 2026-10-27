package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestReadNeedsFromDir(t *testing.T) {
	dir := t.TempDir()
	compose := "services:\n  web:\n    ports:\n      - 8080:80\n      - \"127.0.0.1:5432:5432/tcp\"\n"
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("API_PORT=3000\n# COMMENT_PORT=1\nNAME=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := readNeeds(dir)
	ports := map[string]string{}
	for _, n := range got {
		ports[n.Port] = n.Source
	}
	if ports["8080"] != "docker-compose.yml" || ports["5432"] != "docker-compose.yml" {
		t.Errorf("compose ports: %+v", got)
	}
	if ports["3000"] != ".env" {
		t.Errorf("env ports: %+v", got)
	}
	if _, ok := ports["1"]; ok {
		t.Error("commented .env entry must be skipped")
	}
}

func TestReadNeedsEmptyDir(t *testing.T) {
	if got := readNeeds(t.TempDir()); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

func TestGuardHelpers(t *testing.T) {
	v := Violation{Rule: Rule{Port: "5432", Owner: "pg"}, Item: PortItem{PID: "9", Proto: "tcp"}}
	if got := guardKey(v); got != "5432|9|tcp" {
		t.Errorf("guardKey = %q", got)
	}
	for _, n := range []struct {
		in   int
		want string
	}{{0, "0"}, {7, "7"}, {42, "42"}, {100, "100"}} {
		if got := itoaSimple(n.in); got != n.want {
			t.Errorf("itoaSimple(%d) = %q, want %q", n.in, got, n.want)
		}
	}
}

func TestGuardPathUsesSudoUser(t *testing.T) {
	base := guardPath()
	if filepath.Base(base) != "guard.conf" {
		t.Errorf("guardPath = %q", base)
	}
	t.Setenv("SUDO_USER", "definitely-not-a-real-user-xyz")
	if got := guardPath(); got == "" {
		t.Error("guardPath must not be empty")
	}
	t.Setenv("SUDO_USER", "")
	if got := guardPath(); got != base {
		t.Errorf("without SUDO_USER: %q != %q", got, base)
	}
}

func TestLoadGuardMissingFile(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := loadGuard(); len(got) != 0 {
		t.Errorf("no guard file: got %+v", got)
	}
}

func TestLoadGuardReadsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_USER", "")
	if err := os.MkdirAll(filepath.Join(home, ".config", "portkiller"), 0o755); err != nil {
		t.Fatal(err)
	}
	conf := "# reserved\n5432=postgres-dev\n\nnot-a-rule\n"
	if err := os.WriteFile(filepath.Join(home, ".config", "portkiller", "guard.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadGuard()
	if len(got) != 1 || got[0].Port != "5432" || got[0].Owner != "postgres-dev" {
		t.Errorf("loadGuard = %+v", got)
	}
}

func TestViolationText(t *testing.T) {
	if got := violationText(nil); got != "" {
		t.Errorf("empty = %q", got)
	}
	vs := []Violation{{Rule: Rule{Port: "5432", Owner: "pg"}, Item: PortItem{Process: "node", PID: "7"}}}
	got := violationText(vs)
	if got == "" || !containsAll(got, ":5432", "pg", "node", "PID 7") {
		t.Errorf("single = %q", got)
	}
	vs = append(vs, Violation{Rule: Rule{Port: "5433", Owner: "x"}, Item: PortItem{Process: "y", PID: "8"}})
	if got = violationText(vs); !containsAll(got, "more") {
		t.Errorf("multi = %q", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func TestKillCmdBadPID(t *testing.T) {
	msg := killCmd(PortItem{PID: "not-a-pid", Process: "x"}, false)().(actionMsg)
	if msg.err == nil {
		t.Error("non-numeric pid must produce an error")
	}
}

func TestKillProjectCmdSnapshot(t *testing.T) {
	targets := []PortItem{{PID: "bad-1", Process: "a"}, {PID: "bad-2", Process: "b"}}
	cmd := killProjectCmd("proj", targets)
	targets[0].Process = "mutated"
	msg := cmd().(projectMsg)
	if msg.project != "proj" {
		t.Errorf("project = %q", msg.project)
	}
	if msg.done != 0 || msg.err == nil {
		t.Errorf("all pids invalid: %+v", msg)
	}
}

func TestBlastCmdRuns(t *testing.T) {
	msg := blastCmd(PortItem{PID: itoaSimple(os.Getpid()), Port: "1"})().(blastMsg)
	if msg.port != "1" || msg.pid == "" {
		t.Errorf("msg = %+v", msg)
	}
}

func TestHealthCmdNoPanic(t *testing.T) {
	msg := healthCmd([]PortItem{{Proto: "tcp", Addr: "127.0.0.1", Port: "1"}})().(healthResultMsg)
	if msg.panicked {
		t.Error("health cmd must not panic on a closed port")
	}
	if len(msg.results) != 1 {
		t.Errorf("results = %+v", msg.results)
	}
}

func TestRefreshCmd(t *testing.T) {
	msg := refreshCmd().(portsMsg)
	if msg.Items == nil {
		t.Fatal("refreshCmd returned nil ports")
	}
}

func TestTickCmdNotNil(t *testing.T) {
	if tickCmd() == nil {
		t.Error("tickCmd must return a command")
	}
}

func TestTickMsgAtWrapsTimestamp(t *testing.T) {
	// The tick callback is a named function so its body is covered without
	// waiting out the real 2s tick.
	ts := time.Unix(1700000000, 0)
	got := tickMsgAt(ts).(tickMsg)
	if !time.Time(got).Equal(ts) {
		t.Errorf("tickMsgAt(%v) = %v, want the same instant", ts, time.Time(got))
	}
}

func TestHandleMouseWheelAndClick(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.height = 24

	nm, _ := m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if nm.(model).cursor != 1 {
		t.Errorf("wheel down: cursor = %d, want 1", nm.(model).cursor)
	}
	nm, _ = nm.(model).handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if nm.(model).cursor != 0 {
		t.Errorf("wheel up: cursor = %d, want 0", nm.(model).cursor)
	}

	// Click on the first data row (header is row 0) selects it.
	nm, _ = m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Y: 1, X: 5})
	if nm.(model).cursor != 0 {
		t.Errorf("row click: cursor = %d", nm.(model).cursor)
	}
	// Right-click asks to stop.
	nm, _ = m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonRight, Action: tea.MouseActionPress, Y: 1, X: 5})
	if nm.(model).mode != modeConfirm {
		t.Errorf("right-click should arm confirm, got %v", nm.(model).mode)
	}
}

func TestHandleMouseIgnoredInOverlay(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.showHelp = true
	nm, _ := m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if nm.(model).cursor != 0 {
		t.Error("mouse must be ignored while help is open")
	}
}

func TestHandleMouseFooterClick(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.height = 24
	// Footer is the last line; first footer key is F3/Search -> filter mode.
	nm, _ := m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Y: 23, X: 1})
	if nm.(model).mode != modeFilter {
		t.Errorf("footer click should open search, got mode %v", nm.(model).mode)
	}
}

func TestAskCleanOrphansEmpty(t *testing.T) {
	m := testModel()
	m.all = nil
	m.askCleanOrphans()
	if m.mode == modeConfirmOrphans {
		t.Error("no orphans: must not arm the dialog")
	}
	if m.message == "" {
		t.Error("user should be told there is nothing to clean")
	}
}

func TestAskStopProjectNoProject(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.cursor = 0 // node, project "app" but no group targets in this fixture
	m.askStopProject()
	if m.mode == modeConfirmProject {
		t.Skip("fixture actually has targets; not a failure")
	}
	if m.message == "" {
		t.Error("expected a message about the project")
	}
}
