package main

import (
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
	// No docker on PATH (or not running): must return nil, not panic.
	if got := GetDockerPorts(); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}

func TestStopContainerError(t *testing.T) {
	// No docker binary: CombinedOutput fails, and the error must be wrapped.
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
	// No docker binary: the start must fail (docker error path).
	err := restartOne(Restartable{Docker: true, ID: "abc", Name: "web"})
	if err == nil {
		t.Fatal("docker start without a docker binary must error")
	}
	if !strings.Contains(err.Error(), "docker") {
		t.Errorf("error = %v", err)
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
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
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

// ---- main.go: recordEvents unknown cause when the cause is stale ----

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
