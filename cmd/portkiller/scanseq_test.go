package main

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestTickSkipsScanWhileOneIsInFlight: a scan slower than the 2s tick
// (docker ps stalling, thousands of pids) must not stack another scan on
// every tick.
func TestTickSkipsScanWhileOneIsInFlight(t *testing.T) {
	withTempHome(t)
	m := testModel()
	nm, _ := m.update(tickMsg(time.Now()))
	m = nm.(model)
	if m.scanSeq != 1 {
		t.Fatalf("first tick must start a scan, seq=%d", m.scanSeq)
	}
	nm, cmd := m.update(tickMsg(time.Now()))
	m = nm.(model)
	if m.scanSeq != 1 || cmd == nil {
		t.Fatalf("second tick while scanning: seq=%d cmd=%v, want no new scan but a new tick", m.scanSeq, cmd)
	}
	nm, _ = m.update(portsMsg{seq: 1, Items: m.all})
	m = nm.(model)
	nm, _ = m.update(tickMsg(time.Now()))
	if nm.(model).scanSeq != 2 {
		t.Fatal("tick after the scan landed must start the next one")
	}
}

// TestStalePortsMsgIsDropped: results landing out of order (an old tick
// scan finishing after the refresh that followed a kill) must not roll the
// table back and log the killed port as "appeared" again.
func TestStalePortsMsgIsDropped(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.seeded = true
	m.scanSeq = 2
	fresh := []PortItem{{Proto: "tcp", Port: "5432", PID: "102", Process: "postgres", Source: "host"}}
	nm, _ := m.update(portsMsg{seq: 2, Items: fresh})
	m = nm.(model)
	nm, _ = m.update(portsMsg{seq: 1, Items: testModel().all})
	m = nm.(model)
	if len(m.all) != 1 {
		t.Fatalf("stale scan applied: %d rows", len(m.all))
	}
	nm, _ = m.update(portsMsg{seq: 1, Err: errTest})
	if nm.(model).isErr {
		t.Error("stale scan error must be ignored too")
	}
	if evs := readEvents(eventsPath(), 10); len(evs) != 1 {
		t.Errorf("events = %+v, want only tcp:3000 vanished from the fresh scan", evs)
	}
}

// TestRefreshKeyAndFinishWorkStampScans: every scan the model starts is
// sequenced, so the results can be ordered.
func TestRefreshKeyAndFinishWorkStampScans(t *testing.T) {
	m := testModel()
	nm, cmd := m.normalKey("r")
	if nm.(model).scanSeq != 1 || cmd == nil {
		t.Fatal("r must start a sequenced scan")
	}
	m2, cmd := nm.(model).finishWork("ok", "err", nil)
	if m2.scanSeq != 2 || cmd == nil {
		t.Fatal("finishWork must start a sequenced scan")
	}
	if msg, ok := cmd().(portsMsg); !ok || msg.seq != 2 {
		t.Fatalf("scan cmd returned %#v", msg)
	}
}

// TestGuardNotifyRunsOffTheUpdateLoop: notify-send can block for seconds
// on a stuck session bus; Update must not wait for it.
func TestGuardNotifyRunsOffTheUpdateLoop(t *testing.T) {
	withTempHome(t)
	old := notifySend
	t.Cleanup(func() { notifySend = old })
	release := make(chan struct{})
	called := make(chan string, 1)
	notifySend = func(title, body string) error {
		called <- body
		<-release
		return nil
	}
	defer close(release)
	m := testModel()
	m.guard = []Rule{{Port: "3000", Owner: "postgres"}}
	done := make(chan tea.Cmd, 1)
	go func() {
		_, cmd := m.update(portsMsg{Items: m.all})
		done <- cmd
	}()
	var cmd tea.Cmd
	select {
	case cmd = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Update blocked on notify-send")
	}
	select {
	case <-called:
		t.Fatal("notify ran inside Update")
	default:
	}
	runAll(cmd)
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("notify cmd never ran")
	}
}

// runAll runs a command and, for batches, its children (each in its own
// goroutine, like bubbletea), ignoring tea.Tick-style blocking ones.
func runAll(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		if b, ok := cmd().(tea.BatchMsg); ok {
			for _, c := range b {
				runAll(c)
			}
		}
	}()
}
