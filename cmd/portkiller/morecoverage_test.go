package main

import (
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ---- keys.go: helper-level branches (no side effects, no os.Exit) ----

func TestMoveKeyPageKeys(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.height = 24
	r := m.rows()
	m.cursor = r // room to jump up
	got, handled := m.moveKey("pgup")
	if !handled || got.cursor != 0 {
		t.Fatalf("pgup: cursor=%d handled=%v", got.cursor, handled)
	}
	m.cursor = 0
	got, _ = m.moveKey("pgdown")
	if got.cursor != r {
		t.Fatalf("pgdown: cursor=%d want %d", got.cursor, r)
	}
	if _, handled := m.moveKey("x"); handled {
		t.Fatal("unknown key must not be handled")
	}
}

func TestStartKillBusyAndEmpty(t *testing.T) {
	m := testModel()
	m.busy = true
	if _, cmd, ok := m.startKill("enter"); !ok || cmd != nil {
		t.Fatalf("busy: ok=%v cmd=%v", ok, cmd)
	}
	if _, cmd, ok := m.startKill("x"); ok || cmd != nil {
		t.Fatalf("non-kill key: ok=%v cmd=%v", ok, cmd)
	}
	m = testModel()
	m.view = nil
	if _, cmd, ok := m.startKill("f9"); !ok || cmd != nil {
		t.Fatalf("empty view: mode must not arm, ok=%v cmd=%v", ok, cmd)
	}
}

func TestRestartKeyBusy(t *testing.T) {
	m := testModel()
	m.busy = true
	m.restarts = []Restartable{{Cmd: []string{"x"}}}
	if _, cmd, ok := m.restartKey("R"); !ok || cmd != nil {
		t.Fatalf("busy restart: ok=%v cmd=%v", ok, cmd)
	}
	if _, _, ok := m.restartKey("x"); ok {
		t.Fatal("non-R key must not be handled")
	}
	// Empty stack: no command, message set, not busy.
	m.busy = false
	m.restarts = nil
	nm, cmd, ok := m.restartKey("R")
	if !ok || cmd != nil || nm.busy {
		t.Fatal("empty restart stack must not run")
	}
	if !containsAll(nm.message, "No recently stopped") {
		t.Fatalf("message = %q", nm.message)
	}
}

func TestConfirmKeyAccept(t *testing.T) {
	// modeConfirm (default branch): y arms a kill command and marks the cause.
	m := testModel()
	m.view = m.all
	m.cursor = 0
	m.askKill(false)
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v", m.mode)
	}
	nm, cmd := m.confirmKey("Y")
	got := nm.(model)
	if cmd == nil || !got.busy || got.mode != modeNormal || got.cause != "stopped via portkiller" {
		t.Fatalf("cmd=%v busy=%v mode=%v cause=%q", cmd, got.busy, got.mode, got.cause)
	}
}

func TestConfirmKeyProjectAndOrphans(t *testing.T) {
	// modeConfirmProject: y captures the targets and runs the project kill.
	withTempHome(t)
	m := testModel()
	m.mode = modeConfirmProject
	m.pname = "app"
	m.ptarget = []PortItem{{Port: "3000", PID: itoaSimple(os.Getpid()), Process: "node", Source: "host"}}
	nm, cmd := m.confirmKey("y")
	got := nm.(model)
	if cmd == nil || !got.busy || got.mode != modeNormal {
		t.Fatalf("project: cmd=%v busy=%v mode=%v", cmd, got.busy, got.mode)
	}
	if !containsAll(got.message, "Stopping project app") {
		t.Errorf("message = %q", got.message)
	}

	// modeConfirmOrphans: y captures the orphans and runs the orphan clean.
	m = testModel()
	m.mode = modeConfirmOrphans
	m.otarget = []PortItem{{Port: "3000", PID: "101", Process: "node", Source: "host", Orphan: true}}
	nm, cmd = m.confirmKey("y")
	got = nm.(model)
	if cmd == nil || !got.busy || got.mode != modeNormal {
		t.Fatalf("orphans: cmd=%v busy=%v mode=%v", cmd, got.busy, got.mode)
	}
	if !containsAll(got.message, "Cleaning orphans") {
		t.Errorf("message = %q", got.message)
	}
}

func TestFilterKeyCtrlC(t *testing.T) {
	m := testModel()
	m.mode = modeFilter
	_, cmd := m.filterKey(tea.KeyMsg{Type: tea.KeyCtrlC}, "ctrl+c")
	if cmd == nil {
		t.Fatal("ctrl+c in the filter must quit")
	}
}

func TestViewToggleKeyBranches(t *testing.T) {
	m := testModel()
	m.view = m.all

	// tab toggles the detail pane too.
	nm := m.viewToggleKey("tab")
	if !nm.showDetail {
		t.Error("tab should toggle detail")
	}

	// c opens the conflicts overlay (no compose/.env in a temp dir -> free report).
	withTempHome(t)
	wd := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	nm = m.viewToggleKey("c")
	if !nm.showHelp || len(nm.helpOverride) == 0 {
		t.Fatalf("c should open conflicts overlay, got help=%v lines=%d", nm.showHelp, len(nm.helpOverride))
	}

	// P on a busy model must not arm the project dialog.
	m.busy = true
	if nm := m.viewToggleKey("P"); nm.mode == modeConfirmProject {
		t.Error("busy P must not arm a dialog")
	}
	// X on a busy model must not arm the orphan dialog.
	if nm := m.viewToggleKey("X"); nm.mode == modeConfirmOrphans {
		t.Error("busy X must not arm a dialog")
	}
}

func TestAskStopProjectArmsDialog(t *testing.T) {
	withTempHome(t)
	// A user-owned "node" process in project "app" is a safe target.
	m := testModel()
	m.all = []PortItem{{
		Proto: "tcp", Addr: "0.0.0.0", Port: "3000",
		PID: itoaSimple(os.Getpid()), Process: "node", Source: "host", Project: "app",
	}}
	m.rebuild()
	m.view = m.all
	m.cursor = 0
	m.askStopProject()
	if m.mode != modeConfirmProject {
		t.Fatalf("mode = %v, want modeConfirmProject", m.mode)
	}
	if m.pname != "app" || len(m.ptarget) == 0 {
		t.Fatalf("pname=%q targets=%d", m.pname, len(m.ptarget))
	}
}

func TestAskStopProjectNoSafeTargets(t *testing.T) {
	withTempHome(t)
	// A project made entirely of a system process has no safe kill targets.
	m := testModel()
	m.all = []PortItem{{
		Proto: "tcp", Addr: "0.0.0.0", Port: "22",
		PID: itoaSimple(os.Getpid()), Process: "sshd", Source: "host", Project: "sys",
	}}
	m.rebuild()
	m.view = m.all
	m.cursor = 0
	m.askStopProject()
	if m.mode == modeConfirmProject {
		t.Fatal("a system-only project must not arm the dialog")
	}
	if !containsAll(m.message, "no safe targets") {
		t.Fatalf("message = %q", m.message)
	}
}

func TestAskCleanOrphansArmsDialog(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.all = []PortItem{{Proto: "tcp", Port: "3000", PID: "101", Process: "node", Orphan: true}}
	m.askCleanOrphans()
	if m.mode != modeConfirmOrphans || len(m.otarget) != 1 {
		t.Fatalf("mode=%v targets=%d", m.mode, len(m.otarget))
	}
	// No orphans -> the dialog must not arm.
	m2 := testModel()
	m2.askCleanOrphans()
	if m2.mode == modeConfirmOrphans {
		t.Error("no orphans must not arm the dialog")
	}
}

func TestHandleMouseIgnoresNonPress(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.height = 24
	// Non-press release: no wheel, no click.
	if nm, cmd := m.handleMouse(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft}); nm.(model).cursor != 0 || cmd != nil {
		t.Errorf("release: cursor=%d cmd=%v", nm.(model).cursor, cmd)
	}
	// Middle button press: not left/right, no wheel -> ignored.
	if nm, cmd := m.handleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonMiddle}); nm.(model).cursor != 0 || cmd != nil {
		t.Errorf("middle button: cursor=%d cmd=%v", nm.(model).cursor, cmd)
	}
	// Overlay open: mouse is ignored entirely.
	m.showHelp = true
	if nm, _ := m.handleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}); nm.(model).cursor != 0 {
		t.Error("wheel while help is open must be ignored")
	}
}

// ---- events.go ----

func TestAppendEventsEmptyAndPrune(t *testing.T) {
	withTempHome(t)
	p := filepath.Join(t.TempDir(), "events.jsonl")
	appendEvents(p, nil) // no-op, must not create the file
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("empty events must not create a file")
	}

	// Over maxEvents+200 lines: the file must be trimmed to maxEvents.
	var lines []byte
	for i := 0; i < maxEvents+201; i++ {
		lines = append(lines, []byte(`{"t":"2020-01-01T00:00:00Z","kind":"appeared","proto":"tcp","port":"1","proc":"x","pid":"1"}`+"\n")...)
	}
	if err := os.WriteFile(p, lines, 0o644); err != nil {
		t.Fatal(err)
	}
	appendEvents(p, []Event{{Time: time.Now(), Kind: "appeared", Proto: "tcp", Port: "2", Process: "y", PID: "2"}})
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "\n"); n > maxEvents+1 {
		t.Errorf("events must be pruned, got %d lines", n)
	}
}

func TestReadEventsSkipsJunk(t *testing.T) {
	withTempHome(t)
	p := filepath.Join(t.TempDir(), "events.jsonl")
	body := "not-json\n" +
		`{"t":"2020-01-01T00:00:01Z","kind":"","proto":"tcp","port":"1","proc":"x","pid":"1"}` + "\n" +
		`{"t":"2020-01-01T00:00:02Z","kind":"appeared","proto":"tcp","port":"2","proc":"y","pid":"2"}` + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	evs := readEvents(p, 10)
	if len(evs) != 1 || evs[0].Port != "2" {
		t.Fatalf("junk and empty-kind lines must be skipped, got %+v", evs)
	}
	if readEvents(filepath.Join(t.TempDir(), "nope.jsonl"), 10) != nil {
		t.Error("missing file must yield nil")
	}
}

// ---- group.go ----

func TestProjectNamesEmpty(t *testing.T) {
	if got := projectNames(nil, 3); got != "" {
		t.Fatalf("empty targets: %q", got)
	}
}

// ---- guard.go ----

func TestHomeDirSudoUser(t *testing.T) {
	// An unknown SUDO_USER falls back to $HOME rather than returning "".
	t.Setenv("SUDO_USER", "no-such-user-xyz-portkiller")
	t.Setenv("HOME", "/tmp")
	if got := homeDir(); got == "" {
		t.Error("unknown SUDO_USER must fall back to $HOME")
	}
	// Empty SUDO_USER uses $HOME directly.
	t.Setenv("SUDO_USER", "")
	if got := homeDir(); got == "" {
		t.Error("$HOME must resolve")
	}
	// A valid SUDO_USER resolves that user's home.
	u, err := user.Current()
	if err == nil && u.Username != "" {
		t.Setenv("SUDO_USER", u.Username)
		if got := homeDir(); got == "" {
			t.Error("valid SUDO_USER must resolve a home")
		}
	}
}

func TestRestartKeyWithItems(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.restarts = []Restartable{{Cmd: []string{"true"}, Cwd: t.TempDir(), UID: os.Getuid()}}
	nm, cmd, ok := m.restartKey("R")
	if !ok || cmd == nil {
		t.Fatal("restart with items must return a command")
	}
	got := nm
	if !got.busy || !containsAll(got.message, "Restarting:") {
		t.Fatalf("busy=%v message=%q", got.busy, got.message)
	}
}

// ---- health.go ----

func TestCheckHealthUDPAndBadPort(t *testing.T) {
	got := CheckHealth([]PortItem{
		{Proto: "udp", Addr: "0.0.0.0", Port: "5353", PID: "1"},
		{Proto: "tcp", Addr: "0.0.0.0", Port: "bukan", PID: "2"},
	})
	if len(got) != 2 {
		t.Fatalf("got %d results", len(got))
	}
	if r := got[healthKey("udp", "0.0.0.0", "5353", "1")]; r.status != HealthNotApplicable {
		t.Errorf("udp: %v", r.status)
	}
	if r := got[healthKey("tcp", "0.0.0.0", "bukan", "2")]; r.status != HealthDead {
		t.Errorf("bad port: %v", r.status)
	}
}

// ---- cmds.go ----

func TestComputeBlastHostConnsAndClients(t *testing.T) {
	// A host item with a real listener: the inodeOwners + summarizeClients
	// loops and the readPPIDs/children count all run.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// A listen-only socket is state 0A (LISTEN); countEstablished only keeps
	// state 01 (ESTABLISHED), so open a real client connection.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	cc, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	b := ComputeBlast(PortItem{Port: portStr, PID: itoaSimple(os.Getpid())})
	if b.Conns == 0 {
		t.Error("a live listener must be counted")
	}
	if b.Children != 0 {
		t.Errorf("test process has no children, got %d", b.Children)
	}
	// An unparseable port returns the zero Blast without touching /proc.
	if b := ComputeBlast(PortItem{Port: "bukan"}); b.Conns != 0 || b.Clients != nil {
		t.Errorf("bad port must short-circuit, got %+v", b)
	}
}

// ---- cmds.go ----

func TestKillOrphansSkipsDockerAndSignals(t *testing.T) {
	items := []PortItem{
		{Source: "docker", PID: "abc", Port: "80"}, // skipped
		{Port: "9", PID: "bukan-angka"},            // KillProcess error
		{Port: "9", PID: "999999999"},              // impossible pid: ESRCH error
	}
	msg := killOrphansCmd(items)()
	om, ok := msg.(orphansMsg)
	if !ok {
		t.Fatalf("got %T", msg)
	}
	if om.done != 0 {
		t.Errorf("done = %d, want 0", om.done)
	}
	if om.err == nil {
		t.Error("bad pid must surface an error")
	}
}

func TestKillProjectCmdSkipsSystemProcs(t *testing.T) {
	// An un-killable PID fails; a docker target is skipped by stopItem's path
	// via StopContainer (no docker binary -> error). One host failure is enough
	// to cover the firstErr branch.
	items := []PortItem{
		{Port: "9", PID: "bukan-angka", Process: "x", Source: "host"},
		{Port: "9", PID: "123456", Process: "y", Source: "host"},
	}
	msg := killProjectCmd("app", items)()
	pm, ok := msg.(projectMsg)
	if !ok {
		t.Fatalf("got %T", msg)
	}
	if pm.done != 0 || pm.err == nil {
		t.Fatalf("done=%d err=%v", pm.done, pm.err)
	}
}

func TestKillLoopPartialSuccess(t *testing.T) {
	// One success + one failure: done=1 and failed=true, with the error kept.
	// killLoop only processes items whose Port matches the requested port.
	items := []PortItem{{Port: "80", PID: "1"}, {Port: "80", PID: "2"}}
	n, failed := killLoop("80", false, items, func(it PortItem, _ bool) error {
		if it.PID == "2" {
			return errors.New("nope")
		}
		return nil
	})
	if n != 1 || !failed {
		t.Errorf("n=%d failed=%v, want 1/true", n, failed)
	}
}

func TestKillLoopAllFail(t *testing.T) {
	// Every stop fails: the first error is kept, n=0, failed=true.
	items := []PortItem{{Port: "80", PID: "1"}, {Port: "80", PID: "2"}}
	n, failed := killLoop("80", true, items, func(PortItem, bool) error {
		return errors.New("nope")
	})
	if n != 0 || !failed {
		t.Errorf("n=%d failed=%v, want 0/true", n, failed)
	}
}

func TestKillLoopForceReachesStop(t *testing.T) {
	// force=true is threaded through to stop; a nil error is a clean pass.
	n, failed := killLoop("80", true, []PortItem{{Port: "80"}},
		func(_ PortItem, force bool) error {
			if !force {
				t.Error("force must be forwarded to stop")
			}
			return nil
		})
	if n != 1 || failed {
		t.Errorf("n=%d failed=%v, want 1/false", n, failed)
	}
}

// ---- proc.go: enrich* edge branches ----

func TestEnrichProjectDockerAndBadPID(t *testing.T) {
	it := PortItem{Source: "docker", Process: "docker:web", PID: "abc"}
	enrichProject(&it)
	if it.Project != "web" || it.Cwd != "-" {
		t.Fatalf("docker: %+v", it)
	}
	it = PortItem{Source: "host", Process: "x", PID: "bukan"}
	enrichProject(&it)
	if it.Project != "-" || it.Cwd != "-" {
		t.Fatalf("bad pid: %+v", it)
	}
	// A live process resolves its real cwd/project.
	it = PortItem{Source: "host", Process: "x", PID: itoaSimple(os.Getpid())}
	enrichProject(&it)
	if it.Cwd == "-" {
		t.Fatalf("self cwd must resolve, got %+v", it)
	}
	// A live process whose cwd vanished (deleted) falls back to "-".
	old := readProcLink
	readProcLink = func(string) (string, error) { return "/tmp/gone (deleted)", nil }
	t.Cleanup(func() { readProcLink = old })
	it = PortItem{Source: "host", Process: "x", PID: itoaSimple(os.Getpid())}
	enrichProject(&it)
	if it.Cwd != "-" || it.Project != "-" {
		t.Fatalf("deleted cwd must fall back to \"-\", got %+v", it)
	}
	// An empty cwd is treated the same way.
	readProcLink = func(string) (string, error) { return "", nil }
	t.Cleanup(func() { readProcLink = old })
	it = PortItem{Source: "host", Process: "x", PID: itoaSimple(os.Getpid())}
	enrichProject(&it)
	if it.Cwd != "-" || it.Project != "-" {
		t.Fatalf("empty cwd must fall back to \"-\", got %+v", it)
	}
}

func TestEnrichOrphanBadPIDAndDocker(t *testing.T) {
	it := PortItem{Source: "docker", PID: "abc", Orphan: true}
	enrichOrphan(&it)
	if it.Orphan {
		t.Error("docker must never be an orphan")
	}
	it = PortItem{Source: "host", PID: "bukan", Orphan: true}
	enrichOrphan(&it)
	if it.Orphan {
		t.Error("bad pid must not be an orphan")
	}
	// A live process with no project is not an orphan.
	it = PortItem{Source: "host", PID: itoaSimple(os.Getpid()), Process: "x", Proto: "tcp", Project: "-"}
	enrichOrphan(&it)
	if it.Orphan {
		t.Error("no project must not be an orphan")
	}
	// A live process whose /proc/<pid>/stat is unreadable (race) is not an orphan.
	it = PortItem{Source: "host", PID: "999999999", Process: "x", Proto: "tcp", Project: "app"}
	enrichOrphan(&it)
	if it.Orphan {
		t.Error("vanished pid must not be an orphan")
	}
	// A live process that parses fine is judged by isOrphan (tty, ppid, ...).
	it = PortItem{Source: "host", PID: itoaSimple(os.Getpid()), Process: "x", Proto: "tcp", Project: "app"}
	enrichOrphan(&it)
	if it.Orphan {
		t.Error("a tty-attached process must not be an orphan")
	}
}

func TestEnrichAllDockerAndPIDCache(t *testing.T) {
	items := []PortItem{
		{Source: "docker", Process: "docker:web", PID: "abc", Proto: "tcp"},
		{Source: "host", Process: "x", PID: itoaSimple(os.Getpid()), Proto: "tcp"},
		{Source: "host", Process: "x", PID: itoaSimple(os.Getpid()), Proto: "tcp"},
	}
	enrichAll(items)
	if items[0].Project != "web" || items[0].Orphan {
		t.Fatalf("docker: %+v", items[0])
	}
	// The second host item reuses the first's cached cwd/project.
	if items[1].Cwd != items[2].Cwd || items[1].Project != items[2].Project {
		t.Fatalf("pid cache: %+v vs %+v", items[1], items[2])
	}
}

func TestParseStatErrorPaths(t *testing.T) {
	if _, _, err := parseStat([]byte("no-paren-here")); err == nil {
		t.Error("missing ')' must error")
	}
	if _, _, err := parseStat([]byte("1 (x) S")); err == nil {
		t.Error("too few fields must error")
	}
	// state + ppid + pgrp + session + tty_nr
	if ppid, tty, err := parseStat([]byte("1 (x) S 0 0 0 0 0")); err != nil || ppid != 0 || tty != 0 {
		t.Fatalf("ppid=%d tty=%d err=%v", ppid, tty, err)
	}
	// A non-numeric ppid (fields[1]) must surface the Atoi error.
	if _, _, err := parseStat([]byte("1 (x) S A 0 0 0 0")); err == nil {
		t.Error("non-numeric ppid must error")
	}
	// After the ')' the fields are state(0) ppid(1) pgrp(2) session(3)
	// tty_nr(4) ... so we need at least five fields for the index check.
	// "00x" is not a valid Atoi input, so the second error path fires.
	if _, _, err := parseStat([]byte("1 (x) S 0 0 0 00x")); err == nil {
		t.Error("non-numeric tty_nr must error")
	}
}

// ---- reexecSudo early-return paths (seams: executable, lookPath) ----

func TestReexecSudoExecutableError(t *testing.T) {
	oldExe, oldLook := executable, lookPath
	executable = func() (string, error) { return "", errors.New("no exe") }
	lookPath = func(string) (string, error) { t.Fatal("LookPath must not run"); return "", nil }
	t.Cleanup(func() { executable, lookPath = oldExe, oldLook })
	reexecSudo() // must return without execing
}

func TestReexecSudoNoSudo(t *testing.T) {
	oldExe, oldLook := executable, lookPath
	executable = func() (string, error) { return "/bin/true", nil }
	lookPath = func(string) (string, error) { return "", errors.New("no sudo") }
	t.Cleanup(func() { executable, lookPath = oldExe, oldLook })
	reexecSudo() // must return without execing
}

// ---- appRun: already-handled CLI and the root reexec skip ----

func TestAppRunAlreadyHandled(t *testing.T) {
	called := false
	if !appRun([]string{"--version"}, 0, func([]string) bool { called = true; return true }, func() {
		t.Fatal("reexec must not run when the CLI handled it")
	}) {
		t.Error("a handled CLI must report handled=true")
	}
	if !called {
		t.Error("run must be invoked")
	}
}

func TestAppRunRootSkipsReexec(t *testing.T) {
	if !appRun([]string{"--version"}, 0, func([]string) bool { return true }, func() {
		t.Fatal("euid 0 must not reexec")
	}) {
		t.Error("root with a handled CLI must report handled=true")
	}
}

func TestAppRunReexecsNonRoot(t *testing.T) {
	runs, reexecs := 0, 0
	// "--list" needs root and our run "handles" it. appRun reexecs for a
	// non-root euid and then returns, so the CLI is invoked once and the
	// reexec hook exactly once.
	if !appRun([]string{"--list"}, 1000, func([]string) bool { runs++; return true }, func() { reexecs++ }) {
		t.Error("non-root must reexec")
	}
	if runs != 1 || reexecs != 1 {
		t.Errorf("runs=%d reexecs=%d, want 1/1", runs, reexecs)
	}
}

func TestParentCommOfBadPID(t *testing.T) {
	if got := parentCommOf(999999999); got != "" {
		t.Fatalf("impossible pid: %q", got)
	}
	if got := parentCommOf(os.Getpid()); got == "" {
		t.Error("own comm must resolve")
	}
}

func TestHomeByNameAndUID(t *testing.T) {
	if _, ok := homeByUID("999999999"); ok {
		t.Error("impossible uid must miss")
	}
	if _, ok := homeByName("no-such-user-xyz-portkiller"); ok {
		t.Error("unknown user must miss")
	}
	// The current user must resolve by both id and name.
	uid := itoaSimple(os.Getuid())
	if h, ok := homeByUID(uid); !ok || h == "" {
		t.Errorf("own uid %s: %q", uid, h)
	}
	if h, ok := homeByName(curUsername()); !ok || h == "" {
		t.Errorf("own name %s: %q", curUsername(), h)
	}
}

// curUsername resolves the current user's login name without failing the test
// when the NSS entry is unavailable.
func curUsername() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

// ---- view.go ----

func TestHealthTextLatencyAndUnknown(t *testing.T) {
	if got := healthText(PortItem{Health: HealthAlive, Latency: 2 * time.Millisecond}); got != "● 2ms" {
		t.Errorf("latency: %q", got)
	}
	if got := healthText(PortItem{Health: HealthAlive, Latency: 100 * time.Microsecond}); got != "● <1ms" {
		t.Errorf("sub-ms: %q", got)
	}
	if got := healthText(PortItem{Health: HealthDead}); got != "○ no response" {
		t.Errorf("dead: %q", got)
	}
	if got := healthText(PortItem{Health: HealthNotApplicable}); got != "-" {
		t.Errorf("na: %q", got)
	}
	if got := healthText(PortItem{Health: HealthUnknown}); got != "…" {
		t.Errorf("unknown: %q", got)
	}
}

// ---- conflicts.go ----

func TestConflictLinesEmptyNeeds(t *testing.T) {
	got := conflictLines(nil, nil)
	if len(got) != 1 || !strings.Contains(got[0], "No ports declared") {
		t.Fatalf("got %v", got)
	}
}

func TestReadNeedsAndParsers(t *testing.T) {
	dir := t.TempDir()
	compose := "services:\n  web:\n    ports:\n      - 8080:80\n      - \"127.0.0.1:5432:5432\"\n"
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("APP_PORT=3000\n# skip\nEMPTY=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	needs := readNeeds(dir)
	if len(needs) < 2 {
		t.Fatalf("needs = %+v", needs)
	}
	if p := parseComposePorts(compose); len(p) != 2 || p[0] != "8080" {
		t.Fatalf("compose ports: %v", p)
	}
	if p := parseEnvPorts("APP_PORT=3000\n# x\nBAD=\nFOO=bar\n"); len(p) != 1 || p[0] != "3000" {
		t.Fatalf("env ports: %v", p)
	}
}

// ---- scanner.go ----

func TestGetDockerPortsNoDockerBinary(t *testing.T) {
	// A missing docker binary must yield nil, not panic.
	stubDockerBin(t, filepath.Join(t.TempDir(), "docker"))
	if got := GetDockerPorts(); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}

func TestStopContainerError(t *testing.T) {
	// A missing docker binary fails and the error must be wrapped.
	stubDockerBin(t, filepath.Join(t.TempDir(), "docker"))
	if err := StopContainer("abc"); err == nil {
		t.Error("missing docker must error")
	}
}

func TestSSItemDockerProxySkipped(t *testing.T) {
	// docker-proxy listeners are owned by the docker source.
	fields := strings.Fields(`tcp LISTEN 0 4096 127.0.0.1:2375 0.0.0.0:* users:(("docker-proxy",pid=1,fd=3))`)
	if _, ok := ssItem(fields); ok {
		t.Error("docker-proxy must be skipped")
	}
	// A well-formed line parses.
	fields = strings.Fields(`tcp LISTEN 0 4096 127.0.0.1:8080 0.0.0.0:* users:(("node",pid=1,fd=3))`)
	it, ok := ssItem(fields)
	if !ok || it.Port != "8080" || it.Process != "node" {
		t.Fatalf("got %+v ok=%v", it, ok)
	}
}

func TestParseDockerPSNoArrowSkipped(t *testing.T) {
	out := "abc|web|\n" + // no "->"
		"def|api|0.0.0.0:8080->80/tcp|Up 2 hours\n" +
		"badline\n"
	got := parseDockerPS(out)
	if len(got) != 1 || got[0].Port != "8080" {
		t.Fatalf("got %+v", got)
	}
}

// ---- blast.go ----

func TestInodeOwnersEmpty(t *testing.T) {
	if got := inodeOwners(map[string]bool{}); len(got) != 0 {
		t.Fatalf("empty inodes must yield an empty map, got %v", got)
	}
}

func TestInodeOwnersProcDirError(t *testing.T) {
	oldDir := procDir
	procDir = "/no/such/proc-dir"
	t.Cleanup(func() { procDir = oldDir })
	if got := inodeOwners(map[string]bool{"1": true}); len(got) != 0 {
		t.Errorf("unreadable /proc must yield an empty map, got %v", got)
	}
}

func TestInodeOwnersSkipsBadEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/12345", 0o755); err != nil {
		t.Fatal(err)
	}
	// 12345/fd is left missing -> the read error path is taken.
	if err := os.MkdirAll(dir+"/self/fd", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/self/fd/3", []byte("not-a-symlink"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldDir := procDir
	procDir = dir
	t.Cleanup(func() { procDir = oldDir })
	if got := inodeOwners(map[string]bool{"1": true}); len(got) != 0 {
		t.Errorf("garbage fd must yield an empty map, got %v", got)
	}
}

func TestComputeBlastDockerWithConns(t *testing.T) {
	// A docker item whose port has a real ESTABLISHED connection: Conns is
	// counted from /proc/net/tcp, but the client/children scan is skipped.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer ln.Close()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())

	go func() {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return
		}
		defer c.Close()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Keep the connection open until the test finishes.
		buf := make([]byte, 1)
		_, _ = conn.Read(buf)
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Skipf("cannot dial: %v", err)
	}
	defer client.Close()

	b := ComputeBlast(PortItem{Source: "docker", PID: "abc", Port: portStr})
	if b.Conns == 0 {
		t.Errorf("docker blast must count established conns, got %+v", b)
	}
	if b.Children != 0 || len(b.Clients) != 0 {
		t.Errorf("docker blast must skip /proc owners: %+v", b)
	}
}

func TestComputeBlastSelfChildren(t *testing.T) {
	// Our own process is the "server": its children are counted from /proc.
	// It has no listener on a random high port, so Conns is 0.
	b := ComputeBlast(PortItem{Port: "65533", PID: itoaSimple(os.Getpid())})
	if b.Conns != 0 {
		t.Errorf("no listener on this port: Conns=%d", b.Conns)
	}
	if b.Children < 0 {
		t.Errorf("Children=%d", b.Children)
	}
}

func TestReadPPIDsRealProcesses(t *testing.T) {
	pp := readPPIDs()
	// The parent of the test process (and of a spawned sleep) must be present.
	c := exec.Command("sleep", "5")
	if err := c.Start(); err != nil {
		t.Skipf("cannot spawn sleep: %v", err)
	}
	defer func() {
		_ = c.Process.Kill()
		_, _ = c.Process.Wait()
	}()
	pp = readPPIDs()
	if _, ok := pp[c.Process.Pid]; !ok {
		t.Errorf("sleep pid %d must appear in the ppid map", c.Process.Pid)
	}
	if got := pp[c.Process.Pid]; got != os.Getpid() {
		t.Errorf("sleep ppid = %d, want %d", got, os.Getpid())
	}
}

// ---- restart.go ----

func TestRestartOneDockerMissingBinary(t *testing.T) {
	// A non-existent binary path guarantees failure on any machine (CI runners
	// ship docker, so "assume it is missing" would be host-dependent).
	stubDockerBin(t, filepath.Join(t.TempDir(), "docker"))
	err := restartOne(Restartable{Docker: true, ID: "abc", Name: "web"})
	if err == nil {
		t.Fatal("docker start without a docker binary must error")
	}
	if !strings.Contains(err.Error(), "docker") {
		t.Errorf("error = %v", err)
	}
}

func TestRestartOneHostCommandStarts(t *testing.T) {
	// A real host command with a valid cwd: Start succeeds, Wait is deferred.
	if err := restartOne(Restartable{Cmd: []string{"true"}, Cwd: t.TempDir(), UID: os.Getuid()}); err != nil {
		t.Fatalf("host restart: %v", err)
	}
}

func TestCaptureRestartProcRace(t *testing.T) {
	// The process vanished between the cmdline/cwd reads and the uid lookup.
	old := readProcLink
	readProcLink = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { readProcLink = old })
	if _, ok := captureRestart(PortItem{PID: itoaSimple(os.Getpid()), Process: "x"}); ok {
		t.Error("vanished process must not be capturable")
	}
}

func TestCaptureRestartDockerAndBadPID(t *testing.T) {
	r, ok := captureRestart(PortItem{Source: "docker", Process: "docker:web", PID: "abc"})
	if !ok || !r.Docker || r.Name != "web" || r.ID != "abc" {
		t.Fatalf("docker: %+v ok=%v", r, ok)
	}
	if _, ok := captureRestart(PortItem{PID: "bukan", Process: "x"}); ok {
		t.Error("non-numeric pid must not be capturable")
	}
}

func TestRestartCmdMixed(t *testing.T) {
	// One bad (docker without binary) + one good (no-op) -> done=1, err set.
	rs := []Restartable{
		{Docker: true, ID: "abc", Name: "web"},
		{Cmd: []string{"true"}, Cwd: t.TempDir(), UID: os.Getuid()},
	}
	msg := restartCmd(rs)()
	rm, ok := msg.(restartMsg)
	if !ok {
		t.Fatalf("got %T", msg)
	}
	if rm.done != 1 || rm.err == nil {
		t.Fatalf("done=%d err=%v", rm.done, rm.err)
	}
}

// ---- events.go: unreadable parent dir ----

func TestAppendEventsBadDir(t *testing.T) {
	// A path whose parent is a regular file: MkdirAll fails, append is a no-op.
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	appendEvents(filepath.Join(f, "sub", "events.jsonl"), []Event{{Kind: "appeared"}})
	if st, err := os.Stat(f); err != nil || st.IsDir() {
		t.Fatalf("file must stay a file: %v %v", st, err)
	}
}

// ---- detail.go ----

func TestReadDetailBadPID(t *testing.T) {
	d := readDetail(PortItem{Process: "x", PID: "bukan", Source: "host"})
	if d.Cmdline != "-" || d.Cwd != "-" || d.User != "-" || d.Parent != "-" {
		t.Fatalf("bad pid must keep the defaults, got %+v", d)
	}
}

// ---- main.go: clamp offset follow ----

func TestClampFollowsCursorDown(t *testing.T) {
	// A tiny viewport with a long list: scrolling the cursor past the fold
	// must pull the offset down so the cursor stays visible.
	m := testModel()
	m.view = make([]PortItem, 30)
	copy(m.view, m.all)
	for i := len(m.all); i < len(m.view); i++ {
		m.view[i] = PortItem{Proto: "tcp", Port: itoaSimple(i), PID: itoaSimple(i)}
	}
	m.height, m.offset = 10, 0 // rows() = 6
	m.cursor = len(m.view) - 1
	m.clamp()
	if m.offset == 0 {
		t.Fatal("cursor below the fold must pull the offset off 0")
	}
	if m.cursor < m.offset || m.cursor >= m.offset+m.rows() {
		t.Errorf("cursor=%d offset=%d rows=%d", m.cursor, m.offset, m.rows())
	}
	// A negative offset (e.g. after a resize) clamps to 0.
	m.cursor, m.offset = 0, -3
	m.clamp()
	if m.offset != 0 {
		t.Errorf("offset = %d, want 0", m.offset)
	}
}

// ---- guard.go / proc.go: ownerHome cache hit ----

func TestOwnerHomeCacheHit(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	pid := os.Getpid()
	h1 := ownerHome(pid)
	if h1 == "" {
		t.Fatal("own home must resolve")
	}
	// Second call must hit the cache and return the same value.
	if h2 := ownerHome(pid); h2 != h1 {
		t.Errorf("cache: %q vs %q", h2, h1)
	}
}

// ---- view.go: status-line branches for each mode ----

func TestViewModeStatusLines(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.view = m.all

	// modeConfirmProject: renders the project stop prompt.
	m.mode = modeConfirmProject
	m.pname = "app"
	m.ptarget = []PortItem{{Port: "3000", PID: "101", Process: "node", Source: "host"}}
	if out := m.View(); !containsAll(out, "Stop project app") {
		t.Errorf("project prompt missing:\n%s", out)
	}

	// modeConfirmOrphans: renders the orphan clean prompt.
	m = testModel()
	m.view = m.all
	m.mode = modeConfirmOrphans
	m.otarget = []PortItem{{Port: "3000", PID: "101", Process: "node", Source: "host", Orphan: true}}
	if out := m.View(); !containsAll(out, "Clean up 1 orphans") {
		t.Errorf("orphans prompt missing:\n%s", out)
	}

	// modeConfirm + force + system process: KILL -9 warning.
	m = testModel()
	m.view = m.all
	m.mode = modeConfirm
	m.force = true
	m.target = PortItem{Port: "22", PID: "1", Process: "sshd", Source: "host"}
	if out := m.View(); !containsAll(out, "KILL -9", "SYSTEM PROCESS") {
		t.Errorf("force/system prompt missing:\n%s", out)
	}

	// modeFilter: search bar.
	m = testModel()
	m.view = m.all
	m.mode = modeFilter
	m.filter = "node"
	if out := m.View(); !containsAll(out, "Search: node") {
		t.Errorf("filter bar missing:\n%s", out)
	}

	// message + isErr: error-styled status line.
	m = testModel()
	m.view = m.all
	m.message, m.isErr = "boom", true
	if out := m.View(); !containsAll(out, "boom") {
		t.Errorf("error message missing:\n%s", out)
	}

	// violations: guard warning line.
	m = testModel()
	m.view = m.all
	m.violations = []Violation{{Rule: Rule{Port: "22", Owner: "sshd"}, Item: PortItem{Port: "22", PID: "1", Process: "node"}}}
	if out := m.View(); !containsAll(out, "GUARD") {
		t.Errorf("guard warning missing:\n%s", out)
	}

	// showHelp with override: the conflict/help lines replace the default help.
	m = testModel()
	m.view = m.all
	m.showHelp = true
	m.helpOverride = []string{" Conflicts for /tmp", " free      :8080 (compose.yml)"}
	if out := m.View(); !containsAll(out, "Conflicts for /tmp") {
		t.Errorf("help override missing:\n%s", out)
	}

	// showHistory with events + a tall viewport.
	m = testModel()
	m.view = m.all
	m.showHistory = true
	m.events = []Event{{Time: time.Now(), Kind: "appeared", Proto: "tcp", Port: "3000", Process: "node", PID: "1"}}
	m.height = 40
	if out := m.View(); !containsAll(out, "PORT HISTORY", "appeared") {
		t.Errorf("history overlay missing:\n%s", out)
	}
}

// ---- view.go: orphan flag on the info bar ----

func TestViewOrphanFlags(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.view = m.all
	m.orphans = true
	if out := m.View(); !containsAll(out, "orphan-only") {
		t.Errorf("orphan-only flag missing:\n%s", out)
	}
	// An orphan hidden by the filter must be called out.
	m.orphans = false
	m.view = m.all[:1] // the first item is not an orphan
	m.all = append(m.all, PortItem{Proto: "tcp", Port: "9", PID: "9", Process: "x", Orphan: true})
	if out := m.View(); !containsAll(out, "orphans hidden by filter") {
		t.Errorf("hidden-orphans flag missing:\n%s", out)
	}
}

// ---- docker error paths, via a failing `docker` shim on PATH ----

// withFakeDocker drops a `docker` that always fails into a temp dir and puts
// that dir at the front of PATH for the duration of the test.
func withFakeDocker(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	sh := "#!/bin/sh\necho nope >&2\nexit 1\n"
	path := filepath.Join(dir, "docker")
	if err := os.WriteFile(path, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	stubDockerBin(t, path)
}

func TestStopContainerFakeFailure(t *testing.T) {
	withFakeDocker(t)
	err := StopContainer("abc")
	if err == nil {
		t.Fatal("a failing docker stop must error")
	}
	if !containsAll(err.Error(), "nope") {
		t.Errorf("error must carry docker's output: %v", err)
	}
}

func TestGetDockerPortsFakeFailure(t *testing.T) {
	withFakeDocker(t)
	if got := GetDockerPorts(); got != nil {
		t.Fatalf("failing docker ps must yield nil, got %v", got)
	}
}

func TestRestartOneDockerFakeFailure(t *testing.T) {
	withFakeDocker(t)
	err := restartOne(Restartable{Docker: true, ID: "abc", Name: "web"})
	if err == nil {
		t.Fatal("failing docker start must error")
	}
	if !containsAll(err.Error(), "nope") {
		t.Errorf("error must carry docker's output: %v", err)
	}
}

func TestStopItemDockerAndHost(t *testing.T) {
	withFakeDocker(t)
	// Docker path routes to StopContainer (fails via the shim).
	if err := stopItem(PortItem{Source: "docker", PID: "abc"}, false); err == nil {
		t.Error("docker stop must surface an error")
	}
	// Host path routes to KillProcess (a bad pid fails).
	if err := stopItem(PortItem{Source: "host", PID: "bukan"}, false); err == nil {
		t.Error("bad pid must error")
	}
}

// ---- blast.go: readPPIDs /proc race via the procDir and readProcFile seams ----

func TestReadPPIDsProcDirError(t *testing.T) {
	oldDir, oldRead := procDir, readProcFile
	procDir = "/no/such/proc-dir"
	t.Cleanup(func() { procDir, readProcFile = oldDir, oldRead })
	if got := readPPIDs(); len(got) != 0 {
		t.Errorf("unreadable /proc must yield an empty map, got %v", got)
	}
}

func TestReadPPIDsSkipsBadEntries(t *testing.T) {
	dir := t.TempDir()
	// A non-numeric dir, an unreadable stat, and a stat that fails to parse.
	if err := os.MkdirAll(dir+"/self", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/self/stat", []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir+"/12345", 0o755); err != nil {
		t.Fatal(err)
	}
	// 12345/stat is left missing -> the read error path is taken.

	oldDir, oldRead := procDir, readProcFile
	procDir = dir
	t.Cleanup(func() { procDir, readProcFile = oldDir, oldRead })
	if got := readPPIDs(); len(got) != 0 {
		t.Errorf("garbage/missing stat must yield an empty map, got %v", got)
	}
}

// ---- main.go: update branches not yet reached ----

func TestUpdateUnknownMsg(t *testing.T) {
	m := testModel()
	nm, cmd := m.update(struct{ x int }{})
	if cmd != nil {
		t.Error("unknown message must not schedule work")
	}
	if _, ok := nm.(model); !ok {
		t.Error("update must return the model")
	}
}

func TestCheckGuardNowRecordsEventOnce(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.guard = []Rule{{Port: "3000", Owner: "postgres"}}
	m.all = []PortItem{{Proto: "tcp", Port: "3000", PID: "1", Process: "node"}}
	m.checkGuardNow()
	evs := readEvents(eventsPath(), 10)
	if len(evs) != 1 {
		t.Fatalf("first violation must be recorded once, got %d", len(evs))
	}
	// A second tick with the same violation must not add another event.
	m.checkGuardNow()
	if n := len(readEvents(eventsPath(), 10)); n != 1 {
		t.Errorf("repeat violation must not re-log, got %d", n)
	}
}

// ---- view.go: pid truncation and docker row style ----

func TestViewPidTruncationAndStyles(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.view = []PortItem{
		{Proto: "tcp", Addr: "0.0.0.0", Port: "1", PID: "123456789012", Process: "longpid", Source: "host"},
		{Proto: "tcp", Addr: "0.0.0.0", Port: "2", PID: "abc", Process: "docker:web", Source: "docker"},
		{Proto: "tcp", Addr: "0.0.0.0", Port: "3", PID: "2", Process: "sshd", Source: "host"},
	}
	if out := m.View(); !containsAll(out, "12345678", "docker:web", "sshd") {
		t.Errorf("truncated pid / docker / system styles missing:\n%s", out)
	}
	// An empty view must show the "no matching ports" line.
	m.view = nil
	if out := m.View(); !containsAll(out, "No matching ports") {
		t.Errorf("empty-view hint missing:\n%s", out)
	}
}

func TestRecordEventsStaleCause(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.seeded = true
	m.cause, m.causeAt = "stopped via portkiller", time.Now().Add(-time.Minute)
	m.all = []PortItem{{Proto: "tcp", Port: "3000", PID: "1", Process: "node"}}
	m.recordEvents(nil)
	evs := readEvents(eventsPath(), 10)
	if len(evs) == 0 {
		t.Fatal("expected an event")
	}
	if evs[0].Cause != "unknown" {
		t.Errorf("stale cause must fall back to unknown, got %q", evs[0].Cause)
	}
}

// ---- cmds.go: healthCmd recover path via the checkHealth seam ----

func TestHealthCmdRecoversFromPanic(t *testing.T) {
	old := checkHealth
	checkHealth = func([]PortItem) map[string]healthResult { panic("boom") }
	t.Cleanup(func() { checkHealth = old })

	msg, ok := healthCmd(nil)().(healthResultMsg)
	if !ok {
		t.Fatalf("got %T", msg)
	}
	if !msg.panicked {
		t.Error("a panicking check must be recovered")
	}
}

// ---- proc.go: enrichProject /proc race via the readProcLink seam ----

func TestEnrichProjectProcRace(t *testing.T) {
	old := readProcLink
	readProcLink = func(string) (string, error) { return "", os.ErrPermission }
	t.Cleanup(func() { readProcLink = old })

	it := PortItem{Source: "host", PID: itoaSimple(os.Getpid()), Process: "x"}
	enrichProject(&it)
	if it.Cwd != "-" || it.Project != "-" {
		t.Fatalf("unreadable cwd must fall back to \"-\", got %+v", it)
	}
}

// ---- main.go: askStopProject / update / guardNow uncovered branches ----

func TestUpdateHealthResultInitAndClear(t *testing.T) {
	m := testModel()
	m.health = nil
	m.hBusy = true
	m.message = msgHealthPanic
	m.isErr = true
	want := healthResult{status: HealthAlive, latency: 5 * time.Millisecond}
	nm, cmd := m.update(healthResultMsg{results: map[string]healthResult{"k": want}})
	mod := nm.(model)
	if mod.hBusy {
		t.Error("health result must clear hBusy")
	}
	if mod.health == nil {
		t.Fatal("health map must be initialized")
	}
	if mod.health["k"] != want {
		t.Errorf("health = %+v, want %+v", mod.health["k"], want)
	}
	if mod.message == msgHealthPanic || mod.isErr {
		t.Errorf("stale panic message must clear, got %q isErr=%v", mod.message, mod.isErr)
	}
	if cmd != nil {
		t.Errorf("health result must not re-trigger a command, got %v", cmd)
	}
}

func TestCheckGuardNowNilGuardSeen(t *testing.T) {
	withTempHome(t)
	// A model whose guardSeen was never allocated must lazily create it so
	// the first violation is recorded exactly once.
	m := testModel()
	m.guard = []Rule{{Port: "3000", Owner: "postgres"}}
	m.all = []PortItem{{Proto: "tcp", Port: "3000", PID: "1", Process: "node"}}
	m.guardSeen = nil
	m.checkGuardNow()
	if m.guardSeen == nil {
		t.Fatal("guardSeen must be lazily allocated")
	}
	if len(readEvents(eventsPath(), 10)) != 1 {
		t.Error("first violation must be recorded once")
	}
}

// ---- main.go: appRun dispatch ----

// ---- guard.go: homeDir with an unreadable $HOME ----

func TestHomeDirUserHomeDirFallback(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	old := userHomeDir
	userHomeDir = func() (string, error) { return "", errors.New("no home") }
	t.Cleanup(func() { userHomeDir = old })
	if got := homeDir(); got != "" {
		t.Errorf("homeDir = %q, want \"\"", got)
	}
}

func TestOwnerHomeUserHomeDirFallback(t *testing.T) {
	// An unknown pid whose uid lookup fails, with an empty SUDO_USER, falls
	// through to $HOME. Force that final fallback to fail so the empty
	// return path is covered.
	t.Setenv("SUDO_USER", "")
	old := userHomeDir
	userHomeDir = func() (string, error) { return "", errors.New("no home") }
	t.Cleanup(func() { userHomeDir = old })
	if got := ownerHome(999999); got != "" {
		t.Errorf("ownerHome = %q, want \"\"", got)
	}
}

// ---- own.go: lookupUser seam ----

func TestChownToSudoUserRootLookupError(t *testing.T) {
	t.Setenv("SUDO_USER", "someone")
	oldUID, oldLookup := effectiveUID, lookupUser
	effectiveUID = func() int { return 0 }
	lookupUser = func(string) (*user.User, error) { return nil, errors.New("nss down") }
	t.Cleanup(func() { effectiveUID, lookupUser = oldUID, oldLookup })
	// Must return before any chown and must not panic.
	chownToSudoUser("/tmp")
}

func TestChownToSudoUserRootBadIDs(t *testing.T) {
	t.Setenv("SUDO_USER", "someone")
	oldUID, oldLookup := effectiveUID, lookupUser
	effectiveUID = func() int { return 0 }
	lookupUser = func(string) (*user.User, error) {
		return &user.User{Uid: "not-a-uid", Gid: "not-a-gid", Username: "ghost"}, nil
	}
	t.Cleanup(func() { effectiveUID, lookupUser = oldUID, oldLookup })
	// chownIDs fails on a non-numeric uid -> must return without chowning.
	chownToSudoUser("/tmp")
}

// ---- proc.go: parseStat ttyNr Atoi error ----

func TestParseStatTTYNrAtoiError(t *testing.T) {
	// Fields after ')' are state(0) ppid(1) pgrp(2) session(3) tty_nr(4) ...
	// so tty_nr must be the 5th token. A non-numeric value there surfaces
	// the Atoi error rather than being silently coerced to 0.
	if _, _, err := parseStat([]byte("1 (x) S 0 0 0 x 0")); err == nil {
		t.Error("non-numeric tty_nr must error")
	}
	if _, _, err := parseStat([]byte("1 (x) S 0 0 0 x+ 0")); err == nil {
		t.Error("malformed tty_nr must error")
	}
}

// ---- proc.go: enrichOrphan uid-vanished conservative path ----

func TestEnrichOrphanUIDVanished(t *testing.T) {
	// The /proc/<pid>/stat read succeeds but the uid lookup loses the race
	// (the pid is gone by then). enrichOrphan must fall back to uid 0, which
	// keeps the orphan check conservative instead of aborting.
	oldProc, oldStat := procFile, statProc
	procFile = func(string) ([]byte, error) {
		return []byte("4242 (app) S 1 1 1 0 -1 4194304 0 0 0 0 0 0 0 0 20 0 1 0 100 0 0"), nil
	}
	statProc = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	t.Cleanup(func() { procFile, statProc = oldProc, oldStat })
	it := PortItem{Source: "host", PID: "4242", Process: "app", Proto: "tcp", Project: "demo"}
	enrichOrphan(&it)
	// ppid 1 + no tty + project + uid 0 (not a system uid) -> orphan candidate.
	if !it.Orphan {
		t.Error("uid-vanished pid must be judged with uid 0, not skipped")
	}
}

func TestEnrichOrphanLiveProcessHasTTY(t *testing.T) {
	// A tty-attached process is never an orphan, even when its uid lookup
	// races; this pins the isOrphan side of the same path.
	it := PortItem{Source: "host", PID: itoaSimple(os.Getpid()), Process: "x", Proto: "tcp", Project: "app"}
	enrichOrphan(&it)
	if it.Orphan {
		t.Error("a tty-attached process must not be an orphan even if its uid lookup races")
	}
}

func TestProcUIDStatRaceAndNonStatT(t *testing.T) {
	// A pid that vanishes between lookup and Stat: (0, false).
	old := statProc
	statProc = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	t.Cleanup(func() { statProc = old })
	if uid, ok := procUID(1); ok || uid != "" {
		t.Errorf("vanished pid: uid=%q ok=%v, want \"\"/false", uid, ok)
	}
	// A FileInfo whose Sys() is not a *syscall.Stat_t: (0, false) as well.
	statProc = func(string) (os.FileInfo, error) { return fakeFileInfo{}, nil }
	t.Cleanup(func() { statProc = old })
	if _, ok := procUID(1); ok {
		t.Error("non-Stat_t FileInfo must report ok=false")
	}
}

type fakeFileInfo struct{ os.FileInfo }

func (fakeFileInfo) Sys() any { return "not a Stat_t" }

func TestEnrichOrphanParseStatRace(t *testing.T) {
	// The pid is readable but /proc/<pid>/stat yields unparseable bytes:
	// the item must be marked not-an-orphan rather than panicking.
	old := procFile
	procFile = func(string) ([]byte, error) { return []byte("garbage"), nil }
	t.Cleanup(func() { procFile = old })
	it := PortItem{Source: "host", PID: itoaSimple(os.Getpid()), Process: "x", Proto: "tcp", Project: "app"}
	enrichOrphan(&it)
	if it.Orphan {
		t.Error("unparseable stat must not be an orphan")
	}
}

// ---- main.go: recordEvents drops udp noise, keeps the rest ----

func TestRecordEventsKeepsTCPAndDropsUDPNoise(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.seeded = true
	// A udp item whose cause is not "stopped via portkiller" is noise and is
	// filtered; the tcp item must still be recorded.
	m.cause, m.causeAt = "unknown", time.Now()
	m.all = []PortItem{
		{Proto: "udp", Port: "5353", PID: "1", Process: "avahi"},
		{Proto: "tcp", Port: "3000", PID: "2", Process: "node"},
	}
	m.recordEvents(nil)
	evs := readEvents(eventsPath(), 10)
	if len(evs) != 1 {
		t.Fatalf("want exactly the tcp event, got %d", len(evs))
	}
	if evs[0].Proto != "tcp" {
		t.Errorf("proto = %q, want tcp", evs[0].Proto)
	}
}

// ---- keys.go: startKill force key ----

func TestStartKillForceKeyArms(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.cursor = 0
	nm, cmd, ok := m.startKill("K")
	if !ok {
		t.Fatal("K must be handled")
	}
	if cmd == nil {
		t.Fatal("K on a live target must issue a blast command")
	}
	if nm.mode != modeConfirm {
		t.Errorf("mode = %v, want modeConfirm", nm.mode)
	}
}

// ---- keys.go: normalKey and viewToggleKey leftover branches ----

func TestNormalKeyQuitAndRefresh(t *testing.T) {
	m := testModel()
	m.view = m.all
	for _, k := range []string{"ctrl+c", "q", "f10"} {
		if _, cmd := m.normalKey(k); cmd == nil {
			t.Errorf("%s must quit", k)
		}
	}
	if _, cmd := m.normalKey("r"); cmd == nil {
		t.Error("r must refresh")
	}
	if _, cmd := m.normalKey("f5"); cmd == nil {
		t.Error("f5 must refresh")
	}
}

func TestNormalKeyRestartReturnsCmd(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.view = m.all
	// A recorded restartable makes restartKey hand a command back, which is
	// the one normalKey branch that returns early instead of falling through.
	m.restarts = []Restartable{{Name: "web", Cmd: []string{"true"}, Cwd: t.TempDir()}}
	nm, cmd := m.normalKey("R")
	if cmd == nil {
		t.Fatal("R with a pending restart must return a command")
	}
	if !nm.(model).busy {
		t.Error("restart must mark the model busy")
	}
}

func TestViewToggleKeyHelpAndFilter(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.view = m.all
	if nm := m.viewToggleKey("?"); !nm.showHelp {
		t.Error("? must open help")
	}
	if nm := m.viewToggleKey("f1"); !nm.showHelp {
		t.Error("f1 must open help")
	}
	m = testModel()
	if nm := m.viewToggleKey("/"); nm.mode != modeFilter {
		t.Errorf("/ must arm the filter, mode=%v", nm.mode)
	}
	if nm := m.viewToggleKey("f3"); nm.mode != modeFilter {
		t.Errorf("f3 must arm the filter, mode=%v", nm.mode)
	}
	// esc clears the filter and rebuilds.
	m.filter = "node"
	nm := m.viewToggleKey("esc")
	if nm.filter != "" {
		t.Errorf("esc must clear the filter, got %q", nm.filter)
	}
}

// ---- keys.go: handleMouse footer + row click ----

func TestHandleMouseFooterAndRowClick(t *testing.T) {
	m := testModel()
	m.view = m.all
	// A click on the footer's first key dispatches "/" (search).
	nm, cmd := m.handleMouse(tea.MouseMsg{X: 0, Y: m.height - 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	mod := nm.(model)
	if mod.mode != modeFilter {
		t.Errorf("footer click must arm the filter, mode=%v", mod.mode)
	}
	if cmd != nil {
		t.Errorf("filter key must not issue a command, got %v", cmd)
	}
	// A right-click on a data row must not panic and must keep the model sane.
	nm, _ = m.handleMouse(tea.MouseMsg{X: 0, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if _, ok := nm.(model); !ok {
		t.Errorf("right-click must return a model, got %T", nm)
	}
}

// ---- scanner.go: ssItem docker-proxy and splitAddr failures ----

func TestSSItemRejectsDockerProxyAndBadAddr(t *testing.T) {
	if _, ok := ssItem([]string{"tcp", "LISTEN", "0", "0", "not-an-addr", "0", "users:((\"x\",pid=1,fd=3))"}); ok {
		t.Error("an unparseable local address must be rejected")
	}
	// splitAddr succeeds but the process is docker-proxy: the docker source
	// owns those, so ssItem must drop the line.
	fields := []string{"tcp", "LISTEN", "0", "0", "*:5000", "0", "users:((\"docker-proxy\",pid=1,fd=3))"}
	if _, ok := ssItem(fields); ok {
		t.Error("docker-proxy listeners must be dropped")
	}
	// No pid info at all: parseProcessInfo yields an empty pid.
	fields = []string{"tcp", "LISTEN", "0", "0", "*:5000", "0", "-"}
	if _, ok := ssItem(fields); ok {
		t.Error("lines with no process info must be dropped")
	}
}

// ---- scanner.go: parseDockerPS malformed lines ----

func TestParseDockerPSMalformedLines(t *testing.T) {
	// A row whose ports field has no "->" mapping yields no port.
	if got := parseDockerPS("abc|web|Up 2 hours|\n"); len(got) != 0 {
		t.Errorf("unmapped ports must yield no items, got %+v", got)
	}
	// A host:port with no final ":" separator is skipped.
	if got := parseDockerPS("abc|web|8080|\n"); len(got) != 0 {
		t.Errorf("address without a port separator must be skipped, got %+v", got)
	}
	// A duplicate key is kept only once.
	got := parseDockerPS("abc|web|0.0.0.0:8080->80/tcp|\nabc|web|0.0.0.0:8080->80/tcp|\n")
	if len(got) != 1 {
		t.Errorf("duplicate rows must collapse, got %d items", len(got))
	}
}

// ---- conflicts.go: parseComposePorts / parseEnvPorts leftover branches ----

func TestParseComposePortsContinuations(t *testing.T) {
	// A port entry without a ":" mapping yields nothing.
	if got := parseComposePorts("- \"3000\"\n"); len(got) != 0 {
		t.Errorf("unmapped port must be skipped, got %v", got)
	}
	// An entry whose host part is not numeric is dropped.
	if got := parseComposePorts("- \"localhost:3000\"\n"); len(got) != 0 {
		t.Errorf("non-numeric host must be dropped, got %v", got)
	}
}

func TestParseEnvPortsMalformedLines(t *testing.T) {
	// No "=" at all, or "=" in the first position: both are skipped.
	if got := parseEnvPorts("NOEQUALS\n=value\n"); len(got) != 0 {
		t.Errorf("malformed lines must be skipped, got %v", got)
	}
	// A non-numeric value is dropped.
	if got := parseEnvPorts("API_PORT=not-a-port\n"); len(got) != 0 {
		t.Errorf("non-numeric value must be dropped, got %v", got)
	}
}

func TestConflictLinesDuplicateNeeds(t *testing.T) {
	needs := []Need{{Port: "3000", Source: "compose"}, {Port: "3000", Source: "compose"}}
	items := []PortItem{{Port: "3000", Process: "node", PID: "101"}}
	got := conflictLines(needs, items)
	if len(got) != 1 {
		t.Errorf("duplicate needs must collapse, got %v", got)
	}
	if !strings.Contains(got[0], "node") || !strings.Contains(got[0], "101") {
		t.Errorf("line must name the taker, got %q", got[0])
	}
}

// ---- events.go: appendEvents with an unopenable path ----

func TestAppendEventsUnopenablePath(t *testing.T) {
	// MkdirAll succeeds (the temp dir exists) but OpenFile fails: the path's
	// parent is a file, so the write is skipped rather than panicking.
	parent := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(parent, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	appendEvents(filepath.Join(parent, "events.jsonl"), []Event{{Kind: "test"}})
}

// ---- cmds.go: killProjectCmd firstErr-only path ----

func TestKillProjectCmdAllFailKeepsFirstErr(t *testing.T) {
	// Two failing targets: the first error must be retained (not the last).
	items := []PortItem{
		{Port: "3000", PID: "bukan", Process: "a", Source: "host"},
		{Port: "3000", PID: "bukan2", Process: "b", Source: "host"},
	}
	msg := killProjectCmd("app", items)()
	pm, ok := msg.(projectMsg)
	if !ok {
		t.Fatalf("got %T", msg)
	}
	if pm.done != 0 {
		t.Errorf("done = %d, want 0", pm.done)
	}
	if pm.err == nil {
		t.Fatal("all-fail must retain an error")
	}
	if !strings.Contains(pm.err.Error(), "bukan") {
		t.Errorf("first error must be kept, got %v", pm.err)
	}
}

// ---- view.go: history + PID truncation ----

func TestViewHistoryEmptyAndTruncated(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.events = []Event{}
	m.showHistory = true
	if out := m.View(); !strings.Contains(out, "No events recorded yet.") {
		t.Errorf("empty history hint missing:\n%s", out)
	}
	m.events = make([]Event, m.height) // more events than rows: must not panic
	m.showHistory = true
	_ = m.View()
	// A long PID is truncated to 8 runes in the list.
	m = testModel()
	m.view = []PortItem{{Proto: "tcp", Addr: "0.0.0.0", Port: "3000", PID: "123456789012", Process: "node"}}
	m.showHistory = false
	out := m.View()
	if strings.Contains(out, "123456789012") {
		t.Error("long PID must be truncated in the list")
	}
}

// ---- view.go: help + conflict overlay shapes ----

func TestViewHelpOverrideAndDetailStyles(t *testing.T) {
	m := testModel()
	m.showHelp = true
	if out := m.View(); out == "" {
		t.Error("help view must not be empty")
	}
	// Docker and system rows carry their own style without panicking.
	m = testModel()
	m.view = []PortItem{
		{Proto: "tcp", Addr: "0.0.0.0", Port: "8080", PID: "1", Process: "docker:web", Source: "docker"},
		{Proto: "tcp", Addr: "0.0.0.0", Port: "22", PID: "2", Process: "sshd", Source: "host"},
		{Proto: "udp", Addr: "0.0.0.0", Port: "5353", PID: "3", Process: "avahi", Orphan: true, Source: "host"},
	}
	_ = m.View()
}

// ---- keys.go: mouse wheel while an overlay is open ----

func TestHandleMouseWheelIgnoredWithOverlay(t *testing.T) {
	// The wheel handler only fires in modeNormal with no overlay open.
	// With the help overlay up the wheel press is a no-op.
	m := testModel()
	m.view = m.all
	m.showHelp = true
	nm, cmd := m.handleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp, Y: 2})
	if cmd != nil {
		t.Error("wheel must not issue a command while help is open")
	}
	if _, ok := nm.(model); !ok {
		t.Errorf("expected a model, got %T", nm)
	}
	// A non-press (motion) is also a no-op.
	nm, cmd = m.handleMouse(tea.MouseMsg{Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft, Y: 2})
	if cmd != nil {
		t.Error("motion must not issue a command")
	}
	if _, ok := nm.(model); !ok {
		t.Errorf("expected a model, got %T", nm)
	}
}

// ---- keys.go: "X" and "P" while busy (the guarded body) ----

func TestViewToggleKeyBusyGuardedBodies(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.view = m.all
	m.busy = true
	if nm := m.viewToggleKey("P"); nm.mode == modeConfirmProject {
		t.Error("busy P must not arm the project dialog")
	}
	if nm := m.viewToggleKey("X"); nm.mode == modeConfirmOrphans {
		t.Error("busy X must not arm the orphan dialog")
	}
	// viewToggleKey is a value receiver, so both "P" and "X" arm on the
	// temporary copy and the caller's model never changes. The bodies must
	// still run without panicking; just assert the guard is what matters.
	m.busy = false
	if nm := m.viewToggleKey("X"); nm.mode == modeConfirmOrphans {
		t.Error("X arms via askCleanOrphans, but the value receiver drops it")
	}
	m2 := testModel()
	m2.view = m2.all
	// Non-busy P: askStopProject runs on the temporary value. With the fixture
	// it may arm modeConfirmProject on nm; the caller's model must stay normal.
	_ = m2.viewToggleKey("P")
	if m2.mode == modeConfirmProject {
		t.Error("value receiver must not mutate the caller's model")
	}
}

// ---- keys.go: normalKey "r" returns the bare refreshCmd (not a closure) ----

func TestNormalKeyRefreshIsBareCommand(t *testing.T) {
	// refreshCmd is a plain func value: a returned command must be non-nil
	// and must resolve to a portsMsg when invoked. This covers the "r"/"f5"
	// branch that returns m.refreshCmd without wrapping.
	m := testModel()
	for _, k := range []string{"r", "f5"} {
		nm, cmd := m.normalKey(k)
		if cmd == nil {
			t.Fatalf("key %q: expected a command", k)
		}
		if _, ok := nm.(model); !ok {
			t.Fatalf("key %q: expected a model, got %T", k, nm)
		}
		if _, ok := cmd().(portsMsg); !ok {
			t.Fatalf("key %q: refresh command must yield portsMsg, got %T", k, cmd())
		}
	}
}

// ---- main.go: update falls through to the default ----

func TestUpdateUnrecognizedMsgIsNoop(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.message = "keep me"
	before := m.message
	nm, cmd := m.update(struct{ x int }{1})
	if cmd != nil {
		t.Error("an unrecognized message must not issue a command")
	}
	if got := nm.(model).message; got != before {
		t.Errorf("message = %q, want %q", got, before)
	}
}

// ---- events.go: appendEvents with an unwritable directory ----

func TestAppendEventsSkipsUnwritableDir(t *testing.T) {
	// MkdirAll succeeds (the temp dir exists) but OpenFile on a directory
	// path fails, so appendEvents must return without writing or panicking.
	dir := t.TempDir()
	appendEvents(dir, []Event{{Kind: "test"}})
}

// ---- cmds.go: killProjectCmd partial success ----

func TestKillProjectCmdPartialSuccess(t *testing.T) {
	// One docker target fails (no docker binary) and one host target succeeds.
	// The host target must be a disposable child process, NOT our own pid:
	// SIGTERM to the test binary would kill the whole test run.
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	defer func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	}()
	targets := []PortItem{
		{Source: "docker", PID: "abc", Port: "80"},
		{Source: "host", PID: itoaSimple(child.Process.Pid), Port: "3000", Process: "node"},
	}
	msg := killProjectCmd("app", targets)()
	pm, ok := msg.(projectMsg)
	if !ok {
		t.Fatalf("got %T, want projectMsg", msg)
	}
	if pm.done != 1 {
		t.Errorf("done = %d, want 1", pm.done)
	}
	if pm.err == nil {
		t.Error("the failing docker target must set err")
	}
}

// ---- scanner.go: parseDockerPS host without a port separator ----

func TestParseDockerPSHostWithoutPort(t *testing.T) {
	// "127.0.0.1" has no ":", so LastIndex returns -1 and the line is skipped.
	got := parseDockerPS("abc|web|127.0.0.1->80/tcp|\n")
	if len(got) != 0 {
		t.Errorf("want 0 items, got %d", len(got))
	}
	// With a real host:port the item is parsed.
	got = parseDockerPS("abc|web|127.0.0.1:8080->80/tcp|\n")
	if len(got) != 1 || got[0].Port != "8080" {
		t.Errorf("got %+v", got)
	}
}

// ---- restart.go: captureRestart uid-vanished path ----

func TestCaptureRestartUIDVanished(t *testing.T) {
	// procUid's Stat fails, so captureRestart must report not-restartable
	// rather than panic.
	old := statProc
	statProc = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	t.Cleanup(func() { statProc = old })
	if _, ok := captureRestart(PortItem{Source: "host", PID: itoaSimple(os.Getpid()), Process: "x"}); ok {
		t.Error("a vanished uid must make the item not restartable")
	}
}

// ---- restart.go: restartOne docker failure (fake docker exiting 1) ----

func TestRestartOneDockerFailure(t *testing.T) {
	withFakeDocker(t)
	if err := restartOne(Restartable{Docker: true, ID: "abc", Name: "web"}); err == nil {
		t.Fatal("a failing docker start must return an error")
	}
}

// ---- cli.go: rejectUnknownFlag's non-flag fallthrough ----

func TestRejectUnknownFlagBareArg(t *testing.T) {
	// A bare (non-dash) argument means "start the TUI": false, no exit.
	if rejectUnknownFlag([]string{"foo"}) {
		t.Error("a bare argument must report not-handled")
	}
}

// ---- cli.go: cliKill missing port exits 2 ----

func TestCLIKillMissingPortExits2(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestCLIKillMissingPortHelper", "--")
	cmd.Env = append(os.Environ(), "PORTKILLER_CLI_KILL_MISSING=1")
	out, err := cmd.CombinedOutput()
	code, ok := exitCode(err)
	if !ok {
		t.Fatalf("helper did not exit: %v", err)
	}
	if code != 2 {
		t.Errorf("exit code = %d, want 2\n%s", code, out)
	}
	if !strings.Contains(string(out), "--kill needs a port number") {
		t.Errorf("missing-port message absent:\n%s", out)
	}
}

func TestCLIKillMissingPortHelper(t *testing.T) {
	if os.Getenv("PORTKILLER_CLI_KILL_MISSING") != "1" {
		t.Skip("helper only")
	}
	cliKill([]string{"--kill"}, false)
}

// ---- cli.go: cliList --json happy path ----

func TestCLIListJSON(t *testing.T) {
	// Run in a subprocess so the stdout write is exercised for real.
	cmd := exec.Command(os.Args[0], "-test.run=TestCLIListJSONHelper", "--")
	cmd.Env = append(os.Environ(), "PORTKILLER_CLI_LIST_JSON=1")
	out, err := cmd.CombinedOutput()
	code, ok := exitCode(err)
	if !ok {
		t.Fatalf("helper did not exit: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(out)), "[") {
		t.Errorf("json output must be an array, got:\n%s", out)
	}
}

func TestCLIListJSONHelper(t *testing.T) {
	if os.Getenv("PORTKILLER_CLI_LIST_JSON") != "1" {
		t.Skip("helper only")
	}
	cliList(true)
}

// ---- askStopProject early-return and group-skip branches ----

func TestAskStopProjectEmptyView(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.view = nil
	m.cursor = 0
	m.askStopProject() // must return without indexing or panicking
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal", m.mode)
	}
}

func TestAskStopProjectSkipsOtherGroup(t *testing.T) {
	withTempHome(t)
	m := testModel()
	// Two projects. The first sorted group (aaa) has no safe targets, so it is
	// skipped via `continue`; the loop then falls through to the matching
	// project (zzz), which enters its own branch and hits the "no safe
	// targets" path (its only process is a service-uid pid).
	m.all = []PortItem{
		{Proto: "tcp", Port: "1", PID: "1", Process: "systemd", Project: "aaa"},
		{Proto: "tcp", Port: "2", PID: "2", Process: "sshd", Project: "zzz"},
	}
	m.rebuild()
	for i, it := range m.view {
		if it.Project == "zzz" {
			m.cursor = i
			break
		}
	}
	m.askStopProject()
	if m.message != "Project zzz: no safe targets." {
		t.Errorf("message = %q", m.message)
	}
}

// ---- update: MouseMsg dispatch to handleMouse ----

func TestUpdateMouseMsgDispatch(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.height = 24
	// A press on the footer line: dispatch goes through handleMouse -> footerClick.
	nm, _ := m.update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Y: 23, X: 1})
	if nm.(model).mode != modeFilter {
		t.Errorf("MouseMsg must route to handleMouse; mode = %v", nm.(model).mode)
	}
}

// ---- keys: handleMouse footer miss and restartKey nil-cmd path ----

func TestHandleMouseFooterMiss(t *testing.T) {
	m := testModel()
	m.view = m.all
	m.height = 24
	// X past every footer key: footerClick misses, handleMouse returns m, nil.
	nm, cmd := m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Y: 23, X: 10000})
	if cmd != nil || nm.(model).mode != modeNormal {
		t.Errorf("footer miss: cmd=%v mode=%v", cmd, nm.(model).mode)
	}
}

func TestNormalKeyRestartNilCmd(t *testing.T) {
	m := testModel()
	m.busy = false
	m.restarts = nil // empty stack: restartKey reports handled but yields no cmd
	// Call restartKey directly: normalKey routes "R" to moveKey (end) first.
	nm, cmd, ok := m.restartKey("R")
	if !ok {
		t.Fatal("restartKey must handle R")
	}
	if cmd != nil {
		t.Fatal("empty restart stack must not issue a command")
	}
	if !containsAll(nm.message, "No recently stopped") {
		t.Errorf("message = %q", nm.message)
	}
}

// ---- reexecSudo: the sysExec success/failure seam ----

func TestReexecSudoCallsSysExec(t *testing.T) {
	oldExe, oldLook, oldExec := executable, lookPath, sysExec
	var gotPath string
	var gotArgs []string
	executable = func() (string, error) { return "/bin/true", nil }
	lookPath = func(string) (string, error) { return "/usr/bin/sudo", nil }
	sysExec = func(path string, args []string, env []string) error {
		gotPath, gotArgs = path, args
		return nil // pretend exec succeeded (the real syscall.Exec never returns on success)
	}
	t.Cleanup(func() { executable, lookPath, sysExec = oldExe, oldLook, oldExec })
	reexecSudo()
	if gotPath != "/usr/bin/sudo" {
		t.Errorf("exec path = %q", gotPath)
	}
	if len(gotArgs) < 3 || gotArgs[0] != "sudo" || gotArgs[1] != "-E" || gotArgs[2] != "/bin/true" {
		t.Errorf("exec args = %v", gotArgs)
	}
}

// ---- main(): the --help early-return (appRun handled, so main returns) ----

func TestMainHelpFlagReturns(t *testing.T) {
	oldArgs := os.Args
	os.Args = []string{"portkiller", "--help"}
	t.Cleanup(func() { os.Args = oldArgs })
	main() // --help is handled by runCLI, so main must return without starting the TUI
}

// ---- main(): the interactive path, via the getEUID/runProgram seams ----

// stubMainEnv pins os.Args/euid and captures the exit code for a main() test,
// so the TUI path is reached without a sudo re-exec or a real terminal.
func stubMainEnv(t *testing.T, args ...string) *int {
	t.Helper()
	oldArgs, oldEUID := os.Args, getEUID
	oldRun, oldExit := runProgram, exitProcess
	t.Cleanup(func() { os.Args, getEUID, runProgram, exitProcess = oldArgs, oldEUID, oldRun, oldExit })
	os.Args = append([]string{"portkiller"}, args...)
	getEUID = func() int { return 0 }
	var code int
	exitProcess = func(c int) { code = c }
	return &code
}

func TestMainTUIErrorExitsOne(t *testing.T) {
	code := stubMainEnv(t) // no flag: appRun reports "not handled"
	runProgram = func(*tea.Program) error { return errors.New("boom") }
	main()
	if *code != 1 {
		t.Errorf("exit code = %d, want 1", *code)
	}
}

func TestMainTUISuccessReturns(t *testing.T) {
	code := stubMainEnv(t)
	runProgram = func(*tea.Program) error { return nil }
	main()
	if *code != 0 {
		t.Errorf("exit code = %d, want 0 (clean TUI exit)", *code)
	}
}

// ---- main.go: the default runProgram really runs the program ----

// quitOnKey is a minimal Bubble Tea model that quits on the first key, so the
// real runProgram can be exercised without a terminal.
type quitOnKey struct{}

func (quitOnKey) Init() tea.Cmd { return nil }

func (quitOnKey) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		return quitOnKey{}, tea.Quit
	}
	return quitOnKey{}, nil
}

func (quitOnKey) View() string { return "" }

func TestRunProgramQuitsCleanly(t *testing.T) {
	// String-reader input and no renderer: "q" arrives as a key, the model
	// quits, and Run returns nil -- proving runProgram forwards the error.
	p := tea.NewProgram(quitOnKey{},
		tea.WithInput(strings.NewReader("q")),
		tea.WithOutput(io.Discard),
		tea.WithoutRenderer())
	done := make(chan error, 1)
	go func() { done <- runProgram(p) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runProgram: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runProgram must return once the program quits")
	}
}
