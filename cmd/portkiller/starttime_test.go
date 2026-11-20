package main

import (
	"os"
	"strconv"
	"testing"
)

// TestEnrichAllKeepsStartTimeForEveryRow: a pid listening on several sockets
// (tcp and tcp6, or two ports) must carry its start time on every row, or a
// stop of the second row skips the PID-reuse check.
func TestEnrichAllKeepsStartTimeForEveryRow(t *testing.T) {
	me := strconv.Itoa(os.Getpid())
	items := []PortItem{
		{Proto: "tcp", Port: "1", PID: me, Process: "x", Source: "host"},
		{Proto: "tcp6", Port: "1", PID: me, Process: "x", Source: "host"},
		{Proto: "tcp", Port: "2", PID: me, Process: "x", Source: "host"},
	}
	enrichAll(items)
	for i, it := range items {
		if it.startTime == 0 || it.startTime != items[0].startTime {
			t.Errorf("row %d startTime = %d, want %d", i, it.startTime, items[0].startTime)
		}
	}
}
