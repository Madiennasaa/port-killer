package main

import (
	"errors"
	"os"
	"strconv"
	"testing"
)

var errBoom = errors.New("boom")

// ---- own.go ----

func TestChownIDs(t *testing.T) {
	if uid, gid, ok := chownIDs("1000", "1000"); !ok || uid != 1000 || gid != 1000 {
		t.Fatalf("got %d %d %v", uid, gid, ok)
	}
	if _, _, ok := chownIDs("bukan-angka", "1000"); ok {
		t.Fatal("uid rusak harus gagal")
	}
	if _, _, ok := chownIDs("1000", "bukan-angka"); ok {
		t.Fatal("gid rusak harus gagal")
	}
}

func TestChownPaths(t *testing.T) {
	f, err := os.CreateTemp("", "own")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())
	// Own user: a no-op chown to ourselves must succeed.
	uid, gid, ok := chownIDs(strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid()))
	if !ok {
		t.Fatal("current uid must parse")
	}
	if err := chownPaths([]string{f.Name()}, uid, gid); err != nil {
		t.Fatalf("self chown: %v", err)
	}
	if err := chownPaths([]string{"/no/such/path-xyz"}, uid, gid); err == nil {
		t.Fatal("missing path must error")
	}
}

// ---- view.go ----

func TestSysUptimeFrom(t *testing.T) {
	cases := []struct {
		b   []byte
		err error
	}{
		{[]byte("123456.78 23456.0\n"), nil},
		{nil, os.ErrNotExist},
		{[]byte(""), nil},
		{[]byte("bukan-angka\n"), nil},
	}
	for i, c := range cases {
		if got := sysUptimeFrom(c.b, c.err); i == 0 && (got == "?" || got == "") {
			t.Fatalf("case %d: got %q", i, got)
		}
	}
	if got := sysUptimeFrom([]byte("123456.78 1\n"), nil); got != "1d 10h 17m" {
		t.Fatalf("format: %q", got)
	}
}

// ---- conflicts.go ----

func TestConflictDirFrom(t *testing.T) {
	if got := conflictDirFrom("/srv/app", nil); got != "/srv/app" {
		t.Fatalf("got %q", got)
	}
	if got := conflictDirFrom("", os.ErrNotExist); got != "" {
		t.Fatalf("error harus menghasilkan \"\", got %q", got)
	}
}

func TestConflictDirReal(t *testing.T) {
	if conflictDir() == "" {
		t.Fatal("working directory harus bisa ditentukan")
	}
}

// ---- cli.go ----

func TestKillArgs(t *testing.T) {
	if port, force := killArgs([]string{"--kill", "8080", "--force"}); port != "8080" || !force {
		t.Fatalf("got %q %v", port, force)
	}
	if port, force := killArgs([]string{"--kill"}); port != "" || force {
		t.Fatalf("tanpa target harus kosong, got %q %v", port, force)
	}
	if port, _ := killArgs([]string{"--kill", "--force"}); port != "" {
		t.Fatalf("flag sebagai target harus ditolak, got %q", port)
	}
}

func TestKillLoop(t *testing.T) {
	var stopped []string
	stop := func(it PortItem, force bool) error {
		stopped = append(stopped, it.PID)
		if it.PID == "bad" {
			return errBoom
		}
		return nil
	}
	items := []PortItem{
		{Port: "8080", PID: "1"},
		{Port: "8080", PID: "1"}, // duplikat: dilewati
		{Port: "8080", PID: "bad"},
		{Port: "9999", PID: "2"}, // port lain: dilewati
	}
	n, failed := killLoop("8080", true, items, stop)
	if n != 1 || !failed || len(stopped) != 2 {
		t.Fatalf("got n=%d failed=%v stopped=%v", n, failed, stopped)
	}
	n, failed = killLoop("9999", false, items, stop)
	if n != 1 || failed {
		t.Fatalf("port bersih: got n=%d failed=%v", n, failed)
	}
}

// ---- blast.go pure helpers ----

func TestHexPort(t *testing.T) {
	if got := hexPort("0100007F:1F40"); got != 8000 {
		t.Fatalf("got %d", got)
	}
	if got := hexPort("noport"); got != -1 {
		t.Fatalf("tanpa titik dua harus -1, got %d", got)
	}
	if got := hexPort("x:zz"); got != -1 {
		t.Fatalf("hex rusak harus -1, got %d", got)
	}
}

func TestParseProcNetTCPBadPortSkipped(t *testing.T) {
	// A row whose local port fails to parse must be dropped. (hexPort's
	// remote-port error path shares the same branch, which the "x:zz" case
	// above already covers.)
	data := sampleTCP + "   3: 0100007F:zzzz 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 55555 1 0 100 0 0 10 0\n"
	got := parseProcNetTCP([]byte(data))
	if len(got) != 3 {
		t.Fatalf("bad-port row must be dropped, got %d rows: %+v", len(got), got)
	}
	for _, c := range got {
		if c.Inode == "55555" {
			t.Fatal("row with unparseable port must not be kept")
		}
	}
}

func TestReadPPIDsCoversSelf(t *testing.T) {
	pp := readPPIDs()
	if _, ok := pp[os.Getpid()]; !ok {
		t.Fatalf("readPPIDs must include the test process, got %d entries", len(pp))
	}
	if len(pp) == 0 {
		t.Fatal("readPPIDs must not be empty")
	}
}

func TestComputeBlastDockerSkipsProc(t *testing.T) {
	// A docker item returns after counting conns, with no client/children scan.
	b := ComputeBlast(PortItem{Source: "docker", PID: "abc123", Port: "1"})
	if b.Children != 0 || len(b.Clients) != 0 {
		t.Fatalf("docker blast must skip /proc owners: %+v", b)
	}
	// A non-numeric port yields the zero Blast.
	if b := ComputeBlast(PortItem{Port: "bukan-angka"}); b.Conns != 0 || b.Children != 0 || len(b.Clients) != 0 {
		t.Fatalf("bad port: %+v", b)
	}
}

// ---- main.go pure helpers ----

func TestSortNum(t *testing.T) {
	if sortNum("42") != 42 || sortNum("0") != 0 {
		t.Fatal("numeric")
	}
	if sortNum("dockerid") != 0 {
		t.Fatal("non-numeric must sort as 0")
	}
}

func TestRowsAndClamp(t *testing.T) {
	m := model{height: 4}
	if m.rows() != 1 {
		t.Fatalf("degenerate height must clamp to 1 row, got %d", m.rows())
	}
	// clamp resets a cursor that sits past the end of the view.
	m = testModel()
	m.view = m.all
	m.height, m.offset = 24, 0
	m.cursor = 10
	m.clamp()
	if m.cursor != len(m.view)-1 {
		t.Fatalf("cursor past end should clamp, got %d", m.cursor)
	}
	// A negative cursor clamps up to 0, and the offset follows it down.
	m.cursor, m.offset = -5, 2
	m.clamp()
	if m.cursor != 0 || m.offset != 0 {
		t.Fatalf("cursor=%d offset=%d, want 0/0", m.cursor, m.offset)
	}
}

func TestAskKillEmptyView(t *testing.T) {
	m := testModel()
	m.view = nil
	m.askKill(false)
	if m.mode == modeConfirm {
		t.Fatal("empty view must not arm a dialog")
	}
}

func TestAskStopProjectNoTargets(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.view = m.all
	m.cursor = 0
	m.askStopProject()
	// "app" has a live target, so the dialog arms; a project that is entirely
	// a system process yields the no-safe-targets message instead.
	if m.mode != modeConfirmProject && !containsAll(m.message, "no safe targets") {
		t.Fatalf("mode=%v message=%q", m.mode, m.message)
	}
}

func TestAskStopProjectNoProjectRow(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.all = []PortItem{{Proto: "tcp", Port: "9", PID: "9", Process: "x", Project: "-"}}
	m.rebuild()
	m.view = m.all
	m.cursor = 0
	m.askStopProject()
	if m.message != "This row has no project." {
		t.Fatalf("message = %q", m.message)
	}
}

func TestApplyHealthUnknownAndUDP(t *testing.T) {
	withTempHome(t)
	m := testModel()
	m.health = nil // nil map must be initialized without panicking
	m.all = []PortItem{
		{Proto: "tcp", Addr: "0.0.0.0", Port: "1", PID: "1"},
		{Proto: "udp", Addr: "0.0.0.0", Port: "2", PID: "2"},
	}
	m.applyHealth()
	if m.all[0].Health != HealthUnknown {
		t.Errorf("tcp without a result: got %v", m.all[0].Health)
	}
	if m.all[1].Health != HealthNotApplicable {
		t.Errorf("udp: got %v", m.all[1].Health)
	}
}

// ---- appRun: the sudo/CLI orchestration, with dependencies injected ----

func TestAppRunHelpBeforeSudo(t *testing.T) {
	ran, reexeced := 0, 0
	run := func(a []string) bool { ran++; return hasFlag(a, "--help") }
	re := func() { reexeced++ }
	if !appRun([]string{"--help"}, 1000, run, re) {
		t.Fatal("--help must be handled without sudo")
	}
	if reexeced != 0 {
		t.Fatal("--help must not re-exec under sudo")
	}
	if ran != 1 {
		t.Fatalf("run called %d times, want 1", ran)
	}
}

func TestAppRunReexecsWhenNotRoot(t *testing.T) {
	ran, reexeced := 0, 0
	// A non-root process cannot satisfy --list, so the first run reports
	// "not handled", triggering the sudo re-exec, then a second run follows.
	run := func(a []string) bool { ran++; return false }
	re := func() { reexeced++ }
	if appRun([]string{"--list"}, 1000, run, re) {
		t.Fatal("--list as non-root must fall through to the TUI")
	}
	if reexeced != 1 {
		t.Fatalf("reexec called %d times, want 1", reexeced)
	}
	// cliNeedsRoot short-circuits the first run, so run executes only after sudo.
	if ran != 1 {
		t.Fatalf("run called %d times, want 1 (only after sudo)", ran)
	}
}

func TestAppRunSkipsReexecAsRoot(t *testing.T) {
	ran, reexeced := 0, 0
	run := func(a []string) bool { ran++; return true }
	re := func() { reexeced++ }
	if !appRun([]string{"--list"}, 0, run, re) {
		t.Fatal("--list as root must be handled")
	}
	if reexeced != 0 {
		t.Fatal("root must not re-exec")
	}
}

func TestAppRunNoArgsStartsTUI(t *testing.T) {
	reexeced := 0
	run := func(a []string) bool { return false }
	re := func() { reexeced++ }
	if appRun(nil, 0, run, re) {
		t.Fatal("no args must fall through to the TUI")
	}
}

// ---- Init / initialModel ----

func TestInitSchedulesTick(t *testing.T) {
	m := testModel()
	if m.Init() == nil {
		t.Fatal("Init must return the tick command")
	}
}

func TestInitialModelBuilds(t *testing.T) {
	withTempHome(t)
	m := initialModel()
	if m.width != 80 || m.height != 24 {
		t.Fatalf("size = %dx%d", m.width, m.height)
	}
	if m.health == nil {
		t.Fatal("health map must be initialized")
	}
	if m.view == nil {
		t.Fatal("rebuild must produce a view (possibly empty)")
	}
}
