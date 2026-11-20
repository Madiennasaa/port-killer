package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestViewTinyTerminalKeepsHeader: on terminals of 1..6 rows the view never
// renders more rows than exist, and the column header stays on top.
func TestViewTinyTerminalKeepsHeader(t *testing.T) {
	for _, detail := range []bool{false, true} {
		for h := 1; h <= 12; h++ {
			m := testModel()
			m.width, m.height = 100, h
			m.showDetail = detail
			m.clamp()
			out := m.View()
			lines := strings.Split(out, "\n")
			if len(lines) > h {
				t.Errorf("h=%d detail=%v: %d rows rendered", h, detail, len(lines))
			}
			if !strings.Contains(lines[0], "PROTO") {
				t.Errorf("h=%d detail=%v: header not on top: %q", h, detail, lines[0])
			}
		}
	}
}

// TestTinyTerminalFooterClickStillWorks: the footer stays on the last row
// when there is room for it, so mouse clicks keep mapping to it.
func TestTinyTerminalFooterClickStillWorks(t *testing.T) {
	m := testModel()
	m.width, m.height = 100, 3
	lines := strings.Split(m.View(), "\n")
	if !strings.Contains(stripANSI(lines[len(lines)-1]), "Search") {
		t.Fatalf("footer not on last row: %q", lines)
	}
	nm, _ := m.Update(tea.MouseMsg{X: 0, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if nm.(model).mode != modeFilter {
		t.Errorf("footer click on a 3-row terminal did not open search")
	}
}

// TestZeroRowsCursorStaysValid: with no room for table rows, paging and
// clamping keep the offset at the cursor rather than past it.
func TestZeroRowsCursorStaysValid(t *testing.T) {
	m := testModel()
	m.width, m.height = 100, 3
	m.cursor = 1
	m.clamp()
	if m.offset != m.cursor {
		t.Errorf("offset %d, cursor %d", m.offset, m.cursor)
	}
	m.cursor = 0
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if c := nm.(model).cursor; c != 1 {
		t.Errorf("pgdown on 0 rows moved cursor to %d, want 1", c)
	}
}

// TestOneRowTerminalClickHitsNoFooter: with only the header visible, a click
// on row 0 must not be mistaken for a footer key.
func TestOneRowTerminalClickHitsNoFooter(t *testing.T) {
	m := testModel()
	m.width, m.height = 100, 1
	nm, cmd := m.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if nm.(model).mode != modeNormal || cmd != nil {
		t.Errorf("click on a 1-row terminal triggered a footer key")
	}
}
