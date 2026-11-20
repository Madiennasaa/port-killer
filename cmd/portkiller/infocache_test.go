package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestViewDoesNoIOForInfoBar: the info bar's uptime and root flag are cached
// on the model and refreshed by the tick, never read inside View.
func TestViewDoesNoIOForInfoBar(t *testing.T) {
	oldRead, oldUID := mustReadFile, effectiveUID
	t.Cleanup(func() { mustReadFile, effectiveUID = oldRead, oldUID })
	reads, uids := 0, 0
	mustReadFile = func(string) ([]byte, error) { reads++; return []byte("90061.5 1.0\n"), nil }
	effectiveUID = func() int { uids++; return 1000 }

	m := testModel()
	m.width = 200
	_ = m.View()
	if reads != 0 || uids != 0 {
		t.Fatalf("View did I/O: %d uptime reads, %d euid calls", reads, uids)
	}
	nm, _ := m.Update(tickMsg(time.Now()))
	m = nm.(model)
	if reads != 1 || uids != 1 {
		t.Fatalf("tick refreshed %d/%d times, want 1/1", reads, uids)
	}
	out := m.View()
	if !strings.Contains(out, "up: 1d 01h 01m") || !strings.Contains(out, "not root") {
		t.Errorf("cached info not rendered: %q", out)
	}
	if reads != 1 || uids != 1 {
		t.Fatalf("View after tick did I/O: %d/%d", reads, uids)
	}
}

// TestViewInfoBarBeforeFirstTick: a model without cached info shows "?".
func TestViewInfoBarBeforeFirstTick(t *testing.T) {
	m := testModel()
	m.upText = ""
	if !strings.Contains(m.View(), "up: ?") {
		t.Errorf("missing placeholder uptime")
	}
}

// TestInitialModelCachesInfo: the first frame already has the uptime.
func TestInitialModelCachesInfo(t *testing.T) {
	oldRead := mustReadFile
	t.Cleanup(func() { mustReadFile = oldRead })
	mustReadFile = func(string) ([]byte, error) { return nil, errors.New("no proc") }
	m := initialModel()
	if m.upText != "?" {
		t.Errorf("upText = %q", m.upText)
	}
}
