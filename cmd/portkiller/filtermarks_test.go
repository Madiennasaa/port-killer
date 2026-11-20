package main

import (
	"strings"
	"testing"
)

// Marks on rows the filter hides are kept (they come back when the filter
// is cleared) but are not stopped by F9/F8, so the info bar must say they
// exist instead of silently counting only the visible ones.
func TestInfoBarCountsMarksHiddenByFilter(t *testing.T) {
	m := testModel()
	m.marked = map[string]bool{itemKey(m.all[0]): true, itemKey(m.all[1]): true}
	m.filter = "node"
	m.rebuild()
	if got := m.markHiddenInfo(); !strings.Contains(got, "marked: 1 (+1 filtered out)") {
		t.Errorf("info: %q", got)
	}
	m.filter = "avahi"
	m.rebuild()
	if got := m.markHiddenInfo(); !strings.Contains(got, "marked: 0 (+2 filtered out)") {
		t.Errorf("info: %q", got)
	}
	m.filter = ""
	m.rebuild()
	if got := m.markHiddenInfo(); !strings.Contains(got, "marked: 2") || strings.Contains(got, "filtered") {
		t.Errorf("info: %q", got)
	}
}
