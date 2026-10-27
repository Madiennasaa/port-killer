package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeHistory(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func eventLine(t *testing.T, port, pid string, at time.Time) string {
	t.Helper()
	b, err := json.Marshal(Event{Time: at, Kind: "appeared", Proto: "tcp", Port: port, Process: "node", PID: pid})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLoadEvents(t *testing.T) {
	now := time.Now()
	path := writeHistory(t,
		eventLine(t, "3000", "1", now),
		"not-json",
		`{"kind":""}`,
		eventLine(t, "3001", "2", now),
	)
	evs := loadEvents(path)
	if len(evs) != 2 || evs[0].Port != "3000" || evs[1].Port != "3001" {
		t.Errorf("loadEvents = %+v", evs)
	}
	if got := loadEvents(filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Errorf("missing file = %+v, want nil", got)
	}
}

func TestHistoryEventsFiltersAndLimits(t *testing.T) {
	now := time.Now()
	evs := []Event{
		{Port: "3000", PID: "1", Time: now},
		{Port: "4000", PID: "2", Time: now},
		{Port: "3000", PID: "3", Time: now},
		{Port: "3000", PID: "4", Time: now},
	}
	got := historyEvents(evs, "3000", 2)
	if len(got) != 2 || got[0].PID != "4" || got[1].PID != "3" {
		t.Errorf("historyEvents = %+v, want newest-first PIDs 4,3", got)
	}
	if got := historyEvents(evs, "", 3); len(got) != 3 || got[0].PID != "4" {
		t.Errorf("unfiltered = %+v", got)
	}
	if got := historyEvents(evs, "9999", 5); len(got) != 0 {
		t.Errorf("no match = %+v, want empty", got)
	}
}

func TestIntFlag(t *testing.T) {
	args := []string{"--history", "--limit", "5", "--port", "8080"}
	if n, err := intFlag(args, "--limit", 20); err != nil || n != 5 {
		t.Errorf("limit = %d,%v", n, err)
	}
	if n, err := intFlag(args, "--port", 0); err != nil || n != 8080 {
		t.Errorf("port = %d,%v", n, err)
	}
	if n, err := intFlag([]string{"--history"}, "--limit", 20); err != nil || n != 20 {
		t.Errorf("default limit = %d,%v", n, err)
	}
	for _, bad := range [][]string{
		{"--limit"},
		{"--limit", "abc"},
		{"--limit", "--json"},
	} {
		if _, err := intFlag(bad, "--limit", 20); err == nil {
			t.Errorf("intFlag(%v) must fail", bad)
		}
	}
}

func TestFormatHistory(t *testing.T) {
	e := Event{Time: time.Date(2026, 10, 27, 14, 3, 5, 0, time.Local), Kind: "vanished", Proto: "tcp", Port: "3000", Process: "node", Cause: "stopped via portkiller"}
	got := formatHistory(e)
	for _, want := range []string{"2026-10-27 14:03:05", "vanished", "tcp:3000", "node", "stopped via portkiller"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatHistory = %q, missing %q", got, want)
		}
	}
}

func seedHistory(t *testing.T) {
	t.Helper()
	path := eventsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		eventLine(t, "3000", "1", time.Date(2026, 10, 27, 10, 0, 0, 0, time.Local)),
		eventLine(t, "4000", "2", time.Date(2026, 10, 27, 11, 0, 0, 0, time.Local)),
		eventLine(t, "3000", "3", time.Date(2026, 10, 27, 12, 0, 0, 0, time.Local)),
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCLIHistoryPlain(t *testing.T) {
	withTempHome(t)
	seedHistory(t)

	out := captureStdout(t, func() { cliHistory([]string{"--history", "--port", "3000"}, false) })
	if !strings.Contains(out, "12:00:00") || !strings.Contains(out, "10:00:00") {
		t.Errorf("output = %q, want newest first", out)
	}
	if strings.Contains(out, "4000") {
		t.Errorf("output = %q, must filter to port 3000", out)
	}
	if i, j := strings.Index(out, "12:00:00"), strings.Index(out, "10:00:00"); i > j {
		t.Errorf("output is not newest-first:\n%s", out)
	}
}

func TestCLIHistoryJSON(t *testing.T) {
	withTempHome(t)
	seedHistory(t)

	out := captureStdout(t, func() { cliHistory([]string{"--history", "--limit", "2", "--json"}, true) })
	var evs []Event
	if err := json.Unmarshal([]byte(out), &evs); err != nil {
		t.Fatalf("json: %v (%q)", err, out)
	}
	if len(evs) != 2 || evs[0].PID != "3" {
		t.Errorf("events = %+v", evs)
	}
}

func TestCLIHistoryEmptyJSONIsArray(t *testing.T) {
	withTempHome(t)
	out := captureStdout(t, func() { cliHistory([]string{"--history", "--json"}, true) })
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("empty history = %q, want []", out)
	}
}

func TestCLIHistoryExits(t *testing.T) {
	withTempHome(t)
	oldExit := exitProcess
	t.Cleanup(func() { exitProcess = oldExit })
	var code int
	exitProcess = func(c int) { code = c }

	for _, args := range [][]string{
		{"--history", "--limit", "0"},
		{"--history", "--limit", "x"},
		{"--history", "--limit"},
		{"--history", "--port", "0"},
		{"--history", "--port", "70000"},
		{"--history", "--port"},
	} {
		code = 0
		captureStdout(t, func() { cliHistory(args, false) })
		if code != 2 {
			t.Errorf("cliHistory(%v) exit = %d, want 2", args, code)
		}
	}

	code = 0
	captureStdout(t, func() { cliHistory([]string{"--history"}, false) })
	if code != 0 {
		t.Errorf("empty history must exit 0, got %d", code)
	}
}

func TestRunCLIHistory(t *testing.T) {
	withTempHome(t)
	seedHistory(t)
	out := captureStdout(t, func() {
		if !runCLI([]string{"--history", "--limit", "1"}) {
			t.Error("--history must be handled")
		}
	})
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Errorf("limit 1 printed %q", out)
	}
	if cliNeedsRoot([]string{"--history"}) {
		t.Error("--history must not require root")
	}
}
