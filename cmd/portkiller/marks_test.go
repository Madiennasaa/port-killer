package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ---- browser.go ----

func TestPortURL(t *testing.T) {
	for addr, want := range map[string]string{
		"0.0.0.0":      "http://localhost:3000/",
		"*":            "http://localhost:3000/",
		"[::]":         "http://localhost:3000/",
		"":             "http://localhost:3000/",
		"127.0.0.1":    "http://127.0.0.1:3000/",
		"[::1]":        "http://[::1]:3000/",
		"[fe80::1%lo]": "http://[fe80::1]:3000/",
	} {
		if got := portURL(PortItem{Addr: addr, Port: "3000"}); got != want {
			t.Errorf("portURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

// stubBrowser captures what openInBrowser would launch.
func stubBrowser(t *testing.T, err error) *[]string {
	t.Helper()
	old, oldX := startDetached, xdgOpenBin
	t.Cleanup(func() { startDetached, xdgOpenBin = old, oldX })
	xdgOpenBin = "/usr/bin/xdg-open"
	var got []string
	startDetached = func(bin string, args []string) error {
		got = append([]string{bin}, args...)
		return err
	}
	return &got
}

func TestOpenInBrowser(t *testing.T) {
	stubContainerEnv(t, "", "", "/bin/runuser", 1000, "")
	got := stubBrowser(t, nil)
	url, err := openInBrowser(PortItem{Proto: "tcp", Addr: "0.0.0.0", Port: "8080"})
	if err != nil || url != "http://localhost:8080/" || strings.Join(*got, " ") != "/usr/bin/xdg-open http://localhost:8080/" {
		t.Errorf("direct: %q %v %q", url, err, *got)
	}
	// Under sudo the browser must start as the invoking user, never root.
	effectiveUID = func() int { return 0 }
	t.Setenv("SUDO_USER", "alice")
	if _, err := openInBrowser(PortItem{Proto: "tcp", Port: "8080"}); err != nil || (*got)[0] != "/bin/runuser" || (*got)[2] != "alice" {
		t.Errorf("sudo: %v %q", err, *got)
	}
	t.Setenv("SUDO_USER", "ghost")
	if _, err := openInBrowser(PortItem{Proto: "tcp", Port: "8080"}); err == nil {
		t.Error("unknown sudo user must fail")
	}
	if _, err := openInBrowser(PortItem{Proto: "udp", Port: "53"}); err == nil {
		t.Error("UDP must be refused")
	}
}

func TestStartDetached(t *testing.T) {
	if err := startDetached("/bin/true", nil); err != nil {
		t.Errorf("start /bin/true: %v", err)
	}
	if err := startDetached(filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Error("missing binary must fail")
	}
}

func TestOpenSelectedKey(t *testing.T) {
	stubContainerEnv(t, "", "", "", 1000, "")
	stubBrowser(t, nil)
	m := testModel()
	nm, _ := m.handleKey(key("b"))
	if got := nm.(model); got.isErr || !strings.Contains(got.message, "Opened http://localhost:3000/") {
		t.Errorf("open: %q", got.message)
	}
	stubBrowser(t, errors.New("no display"))
	nm, _ = m.handleKey(key("b"))
	if got := nm.(model); !got.isErr || !strings.Contains(got.message, "no display") {
		t.Errorf("failure: %q", got.message)
	}
	empty := model{width: 80, height: 24}
	if got := empty.openSelected(); got.message != "" {
		t.Errorf("empty view must be a no-op: %q", got.message)
	}
}

// ---- hidden.go ----

func TestParseHidden(t *testing.T) {
	got := parseHidden("# comment\n\n node \nchrome\n")
	if len(got) != 2 || !got["node"] || !got["chrome"] {
		t.Errorf("parseHidden = %v", got)
	}
}

func TestLoadSaveHidden(t *testing.T) {
	withTempHome(t)
	if got := loadHidden(); len(got) != 0 {
		t.Errorf("missing file must be empty: %v", got)
	}
	if err := saveHidden(hiddenPath(), map[string]bool{"zed": true, "chrome": true}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(hiddenPath())
	if !strings.HasSuffix(string(b), "chrome\nzed\n") {
		t.Errorf("file not sorted: %q", b)
	}
	if got := loadHidden(); !got["zed"] || !got["chrome"] {
		t.Errorf("round trip: %v", got)
	}
}

func TestSaveHiddenErrors(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveHidden(filepath.Join(blocker, "sub", "hidden"), nil); err == nil {
		t.Error("mkdir under a file must fail")
	}
	if err := saveHidden(dir, nil); err == nil {
		t.Error("writing onto a directory must fail")
	}
}

func TestToggleHiddenAndShowHidden(t *testing.T) {
	withTempHome(t)
	m := testModel()
	nm, _ := m.handleKey(key("H"))
	m = nm.(model)
	if !m.hidden["node"] || len(m.view) != 1 || !strings.HasPrefix(m.message, "Hidden node") {
		t.Fatalf("hide: %v %d %q", m.hidden, len(m.view), m.message)
	}
	if !strings.Contains(m.markHiddenInfo(), "hidden: 1") {
		t.Errorf("info: %q", m.markHiddenInfo())
	}
	nm, _ = m.handleKey(key("U"))
	m = nm.(model)
	if !m.showHidden || len(m.view) != 2 || !strings.Contains(m.markHiddenInfo(), "hidden: 1 shown") {
		t.Fatalf("show hidden: %d %q", len(m.view), m.markHiddenInfo())
	}
	m.width = 120
	if v := m.View(); !strings.Contains(v, "◌") {
		t.Error("hidden row must carry the ◌ marker")
	}
	nm, _ = m.handleKey(key("H")) // cursor is on node again: unhide
	m = nm.(model)
	if m.hidden["node"] || !strings.HasPrefix(m.message, "Unhidden node") {
		t.Errorf("unhide: %v %q", m.hidden, m.message)
	}
	if got := loadHidden(); got["node"] {
		t.Error("unhide must persist")
	}
}

func TestToggleHiddenSaveError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_USER", "")
	// A regular file where ~/.config should be makes MkdirAll fail.
	if err := os.WriteFile(filepath.Join(home, ".config"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := testModel().toggleHidden()
	if !got.isErr || !strings.HasPrefix(got.message, "Hide:") || got.hidden["node"] {
		t.Errorf("save error: %q %v", got.message, got.hidden)
	}
	if empty := (model{width: 80, height: 24}).toggleHidden(); empty.message != "" {
		t.Errorf("empty view must be a no-op: %q", empty.message)
	}
}

// ---- marks (Space) and batch stop ----

func TestToggleMark(t *testing.T) {
	m := testModel()
	// A real terminal sends KeySpace, not a rune message.
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = nm.(model)
	if !m.marked[itemKey(m.view[0])] || m.cursor != 1 {
		t.Fatalf("mark: %v cursor %d", m.marked, m.cursor)
	}
	m.cursor = 0
	m = m.toggleMark()
	if len(m.marked) != 0 {
		t.Errorf("second Space must unmark: %v", m.marked)
	}
	if empty := (model{width: 80, height: 24}).toggleMark(); empty.marked != nil {
		t.Error("empty view must be a no-op")
	}
}

func TestBatchConfirmAndStop(t *testing.T) {
	m := testModel()
	m.marked = map[string]bool{itemKey(m.view[0]): true, itemKey(m.view[1]): true}
	m.width = 120
	if !strings.Contains(m.View(), "✓") || !strings.Contains(m.markHiddenInfo(), "marked: 2") {
		t.Error("marked rows must render ✓ and be counted")
	}
	nm, cmd := m.handleKey(key("K"))
	m = nm.(model)
	if m.mode != modeConfirmBatch || len(m.btarget) != 2 || !m.force || cmd != nil {
		t.Fatalf("confirm: mode %v targets %d force %v", m.mode, len(m.btarget), m.force)
	}
	if v := m.View(); !strings.Contains(v, "KILL -9 2 selected") {
		t.Errorf("confirm line missing:\n%s", v)
	}
	m.force = false
	if v := m.View(); !strings.Contains(v, "Stop 2 selected") {
		t.Errorf("graceful confirm line missing")
	}
	nm, cmd = m.handleKey(key("y"))
	m = nm.(model)
	if cmd == nil || m.marked != nil || !strings.Contains(m.message, "Stopping 2 selected") {
		t.Fatalf("confirm y: %q", m.message)
	}
	// The test PIDs belong to no real process, so every stop fails.
	msg := cmd().(batchMsg)
	if msg.done != 0 || msg.err == nil {
		t.Errorf("batch result: %+v", msg)
	}
}

func TestBatchMsgUpdate(t *testing.T) {
	m := testModel()
	nm, _ := m.update(batchMsg{done: 3})
	if got := nm.(model); got.message != "Stopped 3 selected" {
		t.Errorf("ok: %q", got.message)
	}
	nm, _ = m.update(batchMsg{done: 1, err: errors.New("boom")})
	if got := nm.(model); !got.isErr || !strings.Contains(got.message, "Selected: 1 OK") {
		t.Errorf("err: %q", got.message)
	}
}

func TestStopEachCountsSuccesses(t *testing.T) {
	withFakeDockerOK(t)
	done, err := stopEach([]PortItem{{PID: "a", Source: "docker"}, {PID: "bad"}, {PID: "b", Source: "docker"}}, false)
	if done != 2 || err == nil {
		t.Errorf("stopEach = %d, %v", done, err)
	}
}

func TestPruneMarksAndEsc(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.marked = map[string]bool{itemKey(m.all[0]): true, "tcp|9|999": true}
	nm, _ := m.update(portsMsg{Items: m.all})
	if got := nm.(model); len(got.marked) != 1 || !got.marked[itemKey(m.all[0])] {
		t.Errorf("prune: %v", got.marked)
	}
	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if got := nm.(model); got.marked != nil {
		t.Errorf("esc must clear marks: %v", got.marked)
	}
}
