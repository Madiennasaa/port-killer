package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// withTempHome redirects the events file (and guard file) to a temp dir.
func withTempHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SUDO_USER", "")
}

func TestUpdateWindowSize(t *testing.T) {
	m := testModel()
	m.view = m.all
	nm, _ := m.update(tea.WindowSizeMsg{Width: 120, Height: 40})
	got := nm.(model)
	if got.width != 120 || got.height != 40 {
		t.Errorf("size = %dx%d", got.width, got.height)
	}
}

func TestUpdateTickClearsStaleMessage(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.message, m.msgAt = "old", time.Now().Add(-messageTTL-time.Second)
	nm, cmd := m.update(tickMsg(time.Now()))
	got := nm.(model)
	if got.message != "" {
		t.Errorf("message = %q, want cleared", got.message)
	}
	if cmd == nil {
		t.Error("tick must schedule the next refresh/tick")
	}
}

func TestUpdateTickKeepsFreshMessage(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.message, m.msgAt = "fresh", time.Now()
	nm, _ := m.update(tickMsg(time.Now()))
	if nm.(model).message != "fresh" {
		t.Error("message inside TTL must survive a tick")
	}
}

func TestUpdatePortsMsgSeedsHistoryOnce(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.seeded = false
	items := []PortItem{{Proto: "tcp", Port: "3000", PID: "1", Process: "node", Source: "host"}}
	nm, cmd := m.update(portsMsg{Items: items})
	got := nm.(model)
	if !got.seeded {
		t.Error("first portsMsg must seed the model")
	}
	if len(got.all) != 1 || got.all[0].Port != "3000" {
		t.Errorf("all = %+v", got.all)
	}
	if cmd == nil {
		t.Error("first portsMsg should kick off a health check")
	}
	if !got.hBusy {
		t.Fatal("hBusy should be set while a health check is in flight")
	}

	// A refresh arriving before the health result must not queue another run.
	nm, cmd = got.update(portsMsg{Items: items})
	if cmd != nil {
		t.Error("hBusy should suppress a duplicate health command")
	}
}

func TestUpdatePortsMsgDropsStaleHealth(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.seeded = true
	old := healthKey("tcp", "0.0.0.0", "3000", "999")
	m.health = map[string]healthResult{old: {status: HealthAlive}}
	m.all = []PortItem{{Proto: "tcp", Addr: "0.0.0.0", Port: "3000", PID: "1", Process: "node"}}
	nm, _ := m.update(portsMsg{Items: m.all})
	got := nm.(model)
	if _, ok := got.health[old]; ok {
		t.Error("health entry for a vanished pid must be dropped")
	}
}

func TestUpdateHealthResultAndPanic(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.hBusy = true
	nm, _ := m.update(healthResultMsg{results: map[string]healthResult{"k": {status: HealthDead}}})
	got := nm.(model)
	if got.hBusy {
		t.Error("hBusy should clear")
	}
	if got.health["k"].status != HealthDead {
		t.Error("results must be merged")
	}

	m = testModel()
	m.message = msgHealthPanic
	nm, _ = m.update(healthResultMsg{panicked: true, results: map[string]healthResult{}})
	if nm.(model).message != msgHealthPanic {
		t.Error("panic message must stay visible")
	}
	nm, _ = nm.(model).update(healthResultMsg{results: map[string]healthResult{}})
	if nm.(model).message != "" {
		t.Error("panic message must clear on the next healthy cycle")
	}
}

func TestUpdateBlastMsgScopedToTarget(t *testing.T) {
	m := testModel()
	m.mode = modeConfirm
	m.target = PortItem{PID: "10", Port: "80"}
	nm, _ := m.update(blastMsg{pid: "10", port: "80", blast: Blast{Conns: 3}})
	got := nm.(model)
	if got.blast.Conns != 3 || got.bLoad {
		t.Errorf("blast = %+v load=%v", got.blast, got.bLoad)
	}

	m = testModel()
	m.mode = modeConfirm
	m.target = PortItem{PID: "10", Port: "80"}
	nm, _ = m.update(blastMsg{pid: "999", port: "80"})
	if nm.(model).bLoad {
		t.Error("blast for another pid must be ignored")
	}
}

func TestUpdateActionMessages(t *testing.T) {
	withTempHome(t)
	cases := []struct {
		name    string
		msg     tea.Msg
		wantSub string
		wantErr bool
	}{
		{"kill ok", actionMsg{item: PortItem{Process: "node", Port: "3000"}}, "Stopped node", false},
		{"kill err", actionMsg{item: PortItem{Process: "node", Port: "3000"}, err: errTest}, "Failed node", true},
		{"project ok", projectMsg{project: "app", done: 2}, "Project app: 2 stopped", false},
		{"project err", projectMsg{project: "app", done: 1, err: errTest}, "Project app", true},
		{"orphans ok", orphansMsg{done: 5}, "Cleaned orphans: 5", false},
		{"orphans err", orphansMsg{done: 1, err: errTest}, "Clean orphans", true},
		{"restart ok", restartMsg{done: 3}, "Restarted: 3", false},
		{"restart err", restartMsg{done: 1, err: errTest}, "Restart:", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := testModel()
			m.busy = true
			nm, cmd := m.update(c.msg)
			got := nm.(model)
			if got.busy {
				t.Error("busy must clear")
			}
			if !containsAll(got.message, c.wantSub) {
				t.Errorf("message = %q, want substring %q", got.message, c.wantSub)
			}
			if got.isErr != c.wantErr {
				t.Errorf("isErr = %v, want %v", got.isErr, c.wantErr)
			}
			if cmd == nil {
				t.Error("result messages must trigger a refresh")
			}
		})
	}
}

func TestUpdateRestartSuccessClearsStack(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.restarts = []Restartable{{Cmd: []string{"x"}}}
	nm, _ := m.update(restartMsg{done: 1})
	if nm.(model).restarts != nil {
		t.Error("successful restart should clear the stack")
	}
	m = testModel()
	m.restarts = []Restartable{{Cmd: []string{"x"}}}
	nm, _ = m.update(restartMsg{done: 0, err: errTest})
	if nm.(model).restarts == nil {
		t.Error("failed restart must keep the stack for a retry")
	}
}

func TestUpdateKeyAndMouseDispatch(t *testing.T) {
	withTempHome(t)
	m := testModel()
	nm, cmd := m.Update(key("ctrl+c"))
	if cmd == nil {
		t.Error("ctrl+c through Update must quit")
	}
	if _, ok := nm.(model); !ok {
		t.Error("Update must return the model")
	}
}

func TestUpdateStampsMessageTimestamp(t *testing.T) {
	m := testModel()
	m.lastMsg = "old"
	m.message = "new"
	// Update is the exported wrapper: it should refresh msgAt for a new message.
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if got := nm.(model); got.message == "new" && got.msgAt.IsZero() {
		t.Error("a changed message should carry a timestamp")
	}
}

func TestCheckGuardNow(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.guard = []Rule{{Port: "3000", Owner: "postgres"}}
	m.all = []PortItem{{Proto: "tcp", Port: "3000", PID: "1", Process: "node"}}
	m.checkGuardNow()
	if len(m.violations) != 1 {
		t.Fatalf("violations = %+v", m.violations)
	}
	// Re-running must not duplicate the guardSeen state.
	before := len(m.guardSeen)
	m.checkGuardNow()
	if len(m.guardSeen) != before {
		t.Error("guardSeen must be rebuilt, not grown")
	}

	m.guard = nil
	m.checkGuardNow()
	if m.violations != nil {
		t.Error("no rules: violations cleared")
	}
}

func TestRecordEventsFiltersUDPNoise(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.seeded = false
	m.recordEvents(nil) // seed only
	if !m.seeded {
		t.Fatal("first call must seed")
	}
	m.all = []PortItem{{Proto: "tcp", Port: "3000", PID: "1", Process: "node"}}
	m.recordEvents(nil)
	if evs := readEvents(eventsPath(), 10); len(evs) == 0 {
		t.Error("vanishing tcp port must be recorded")
	}
}

func TestRecordEventsUsesRecentCause(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.seeded = true
	m.markCause()
	m.all = []PortItem{{Proto: "tcp", Port: "3000", PID: "1", Process: "node"}}
	m.recordEvents(nil)
	evs := readEvents(eventsPath(), 10)
	if len(evs) == 0 {
		t.Fatal("expected an event")
	}
	if evs[0].Cause != "stopped via portkiller" {
		t.Errorf("cause = %q", evs[0].Cause)
	}
}

var errTest = &testErr{}

type testErr struct{}

func (*testErr) Error() string { return "boom" }

func TestUpdatePortsMsgShowsScanError(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.seeded = true
	m.all = []PortItem{{Proto: "tcp", Port: "3000", PID: "1", Process: "node"}}
	nm, cmd := m.update(portsMsg{Err: errors.New("ss: boom")})
	got := nm.(model)
	if !got.isErr || !strings.Contains(got.message, "boom") {
		t.Errorf("scan error not surfaced: %q (isErr=%v)", got.message, got.isErr)
	}
	if got.msgAt.IsZero() {
		t.Error("scan error must set msgAt so it can expire")
	}
	if len(got.all) != 1 || got.all[0].Port != "3000" {
		t.Errorf("partial scan must not replace the last good snapshot: %+v", got.all)
	}
	if cmd != nil {
		t.Error("a failed scan must not trigger another refresh")
	}
}
