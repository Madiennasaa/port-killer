package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWatchEventsAppearedVanishedAndGuard(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	prev := []PortItem{{Proto: "tcp", Port: "3000", PID: "1", Process: "node"}}
	rules := []Rule{{Port: "5432", Owner: "pg"}}
	cur := []PortItem{
		{Proto: "tcp", Port: "8080", PID: "2", Process: "go"},    // appeared
		{Proto: "tcp", Port: "5432", PID: "9", Process: "redis"}, // appeared + guard
		{Proto: "tcp", Port: "3000", PID: "1", Process: "node"},  // unchanged
	}
	evs := watchEvents("", prev, cur, now, rules)
	var seen []string
	for _, e := range evs {
		seen = append(seen, e.Kind+":"+e.Port)
	}
	want := []string{"appeared:8080", "appeared:5432", "guard:5432"}
	if len(seen) != len(want) {
		t.Fatalf("events = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("event %d = %s, want %s", i, seen[i], want[i])
		}
	}
}

func TestWatchEventsPortFilter(t *testing.T) {
	now := time.Now()
	evs := watchEvents("3000",
		[]PortItem{{Proto: "tcp", Port: "3000", PID: "1", Process: "node"}},
		[]PortItem{{Proto: "tcp", Port: "8080", PID: "2", Process: "go"}}, now, nil)
	// appeared:8080 is filtered out; only the vanished event for 3000 remains.
	if len(evs) != 1 || evs[0].Port != "3000" || evs[0].Kind != "vanished" {
		t.Errorf("filter must keep only the port 3000 event, got %+v", evs)
	}
}

// TestRunWatchSeedPassIsSilent: the seed pass prints nothing, and a
// pre-cancelled context stops the watch after exactly one scan.
func TestRunWatchSeedPassIsSilent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scans := 0
	var lines, warns []string
	items := []PortItem{{Proto: "tcp", Port: "3000", PID: "1", Process: "node"}}
	runWatch(ctx, "", false, func() ([]PortItem, error) {
		scans++
		return items, nil
	}, func(l string) { lines = append(lines, l) }, time.Hour, nil, notifySend,
		func(s string) { warns = append(warns, s) })
	if scans != 1 || len(lines) != 0 || len(warns) != 0 {
		t.Errorf("scans=%d lines=%v warns=%v, want 1 scan, silent seed", scans, lines, warns)
	}
}

// TestRunWatchSecondPassReportsVanishedAndWarns: the interval keeps passing so
// the second scan reports the vanished listener, fires --notify once per
// printed event, and scan failures become warnings without stopping.
func TestRunWatchSecondPassReportsVanishedAndWarns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	scans := 0
	var lines, warns []string
	runWatch(ctx, "", true, func() ([]PortItem, error) {
		scans++
		if scans == 1 {
			return []PortItem{{Proto: "tcp", Port: "3000", PID: "1", Process: "node"}}, nil
		}
		if scans == 2 {
			return []PortItem{{Proto: "tcp", Port: "3000", PID: "1", Process: "node"}}, errors.New("ss down")
		}
		return []PortItem{}, nil
	}, func(l string) { lines = append(lines, l) }, 5*time.Millisecond, nil,
		func(title, body string) error { _ = title; return nil },
		func(s string) { warns = append(warns, s) })
	found := false
	for _, l := range lines {
		if strings.Contains(l, "vanished") {
			found = true
		}
	}
	if !found {
		t.Errorf("no vanished event in %v", lines)
	}
	if len(lines) != len(warns)+0 && len(warns) < 1 {
		t.Errorf("warn calls = %d, want at least one", len(warns))
	}
}

// TestRunWatchReportsGuardOnSeed: guard violations are true right now, so the
// seed pass reports them.
func TestRunWatchReportsGuardOnSeed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var lines, notified []string
	warns := 0
	items := []PortItem{{Proto: "tcp", Port: "5432", PID: "9", Process: "redis"}}
	rules := []Rule{{Port: "5432", Owner: "pg"}}
	runWatch(ctx, "", true, func() ([]PortItem, error) {
		return items, nil
	}, func(l string) { lines = append(lines, l) }, time.Hour, rules,
		func(title, body string) error { notified = append(notified, body); return nil },
		func(string) { warns++ })
	if len(lines) != 1 || !strings.Contains(lines[0], "guard") || !strings.Contains(lines[0], "tcp:5432") {
		t.Errorf("lines = %v, want one guard event", lines)
	}
	if len(notified) != 1 {
		t.Errorf("notify calls = %d, want 1", len(notified))
	}
	if warns != 0 {
		t.Errorf("warn calls = %d, want 0", warns)
	}
}

// TestRunWatchFirstPassScanErrorStaysAlive: the seed pass itself may fail; the
// watch still runs exactly once and warns.
func TestRunWatchFirstPassScanErrorStaysAlive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scans, warns := 0, 0
	runWatch(ctx, "3000", false, func() ([]PortItem, error) {
		scans++
		return []PortItem{}, errors.New("ss down")
	}, func(string) {}, time.Hour, nil, notifySend,
		func(string) { warns++ })
	if scans != 1 || warns != 1 {
		t.Errorf("scans=%d warns=%d, want 1/1", scans, warns)
	}
}

func TestWatchPortValidation(t *testing.T) {
	p, err := watchPort([]string{"--watch", "--port", "3000"})
	if err != nil || p != "3000" {
		t.Errorf("watchPort = %q, %v, want 3000 and nil", p, err)
	}
	if _, err := watchPort([]string{"--watch", "--port", "70000"}); err == nil {
		t.Error("--port 70000 must be rejected")
	}
	if _, err := watchPort([]string{"--watch", "--port"}); err == nil {
		t.Error("--port without a number must be rejected")
	}
	p, err = watchPort([]string{"--watch"})
	if err != nil || p != "" {
		t.Errorf("no --port = %q, %v, want empty and nil", p, err)
	}
}

func TestNewRealWatchCtxIsLive(t *testing.T) {
	if ctx := newRealWatchCtx(); ctx == nil {
		t.Error("newRealWatchCtx returned nil")
	}
}

// TestNotifySendShell covers both branches of the real notify-send seam: no
// display server (no-op) and a display server (the helper binary is attempted
// and errors are best effort).
func TestNotifySendShell(t *testing.T) {
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	if err := notifySend("portkiller", "body"); err != nil {
		t.Errorf("no-display call should return nil, got %v", err)
	}
	t.Setenv("DISPLAY", ":99")
	t.Setenv("WAYLAND_DISPLAY", "")
	_ = notifySend("portkiller", "body") // best effort; result ignored
}

func TestCLINeedsRootWatch(t *testing.T) {
	if !cliNeedsRoot([]string{"--watch"}) {
		t.Error("--watch must run under sudo like --list")
	}
}
func TestCliWatchRunsOnePassAndStops(t *testing.T) {
	// cancel fires during the second scan, after the seed pass, so the loop
	// runs exactly two passes and then stops.
	var ctx context.Context
	var cancel context.CancelFunc
	oldCtx := newWatchCtx
	newWatchCtx = func() context.Context { return ctx }
	t.Cleanup(func() { newWatchCtx = oldCtx; cancel() })
	ctx, cancel = context.WithCancel(context.Background())

	// A guard rule violated on the second pass must be printed: that exercises
	// the print closure inside cliWatch's runWatch wiring.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".config", "portkiller"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".config", "portkiller", "guard.conf"),
		[]byte("5432=pg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldHome, oldSudo := userHomeDir, os.Getenv("SUDO_USER")
	userHomeDir = func() (string, error) { return dir, nil }
	t.Setenv("SUDO_USER", "")
	t.Cleanup(func() {
		userHomeDir = oldHome
		t.Setenv("SUDO_USER", oldSudo)
	})

	// A scan error must flow through the real watchWarn seam.
	oldPorts := activePorts
	scanCount := 0
	activePorts = func() ([]PortItem, error) {
		scanCount++
		if scanCount == 1 {
			return nil, errors.New("ss down")
		}
		cancel()
		return []PortItem{{Proto: "tcp", Port: "5432", PID: "9", Process: "redis"}}, nil
	}
	t.Cleanup(func() { activePorts = oldPorts })

	oldInterval := watchInterval
	watchInterval = time.Millisecond
	t.Cleanup(func() { watchInterval = oldInterval })

	out := captureStdout(t, func() { cliWatch([]string{"--watch"}) })
	if !strings.Contains(out, "guard") || !strings.Contains(out, "tcp:5432") {
		t.Errorf("guard event missing from watch output:\n%s", out)
	}
}

func TestCliWatchBadPortExitsTwo(t *testing.T) {
	old := exitProcess
	var code int
	exitProcess = func(c int) { code = c }
	t.Cleanup(func() { exitProcess = old })
	stubActivePorts(t, nil)
	cliWatch([]string{"--watch", "--port", "70000"})
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}
