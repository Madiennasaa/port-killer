package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestFlagArgs(t *testing.T) {
	args := []string{"--kill", "3000", "3001", "--kill-name", "node", "", "vite"}
	if got := flagArgs(args, "--kill"); strings.Join(got, ",") != "3000,3001" {
		t.Errorf("--kill: %q", got)
	}
	if got := flagArgs(args, "--kill-name"); strings.Join(got, ",") != "node" {
		t.Errorf("an empty argument must end the values: %q", got)
	}
	if got := flagArgs(args, "--wait"); got != nil {
		t.Errorf("absent flag: %q", got)
	}
}

func TestParseSignal(t *testing.T) {
	for in, want := range map[string]string{"HUP": "SIGHUP", "sigusr1": "SIGUSR1", "SIGTERM": "SIGTERM", "9": "9"} {
		if _, name, err := parseSignal(in); err != nil || name != want {
			t.Errorf("parseSignal(%q) = %q, %v", in, name, err)
		}
	}
	if sig, _, _ := parseSignal("int"); sig != syscall.SIGINT {
		t.Errorf("int = %v", sig)
	}
	for _, bad := range []string{"0", "65", "BOGUS"} {
		if _, _, err := parseSignal(bad); err == nil {
			t.Errorf("parseSignal(%q) must fail", bad)
		}
	}
}

func TestParseKillOpts(t *testing.T) {
	o, err := parseKillOpts([]string{"--kill", "1", "--signal", "hup", "--wait"}, false)
	if err != nil || o.sig != syscall.SIGHUP || o.sigName != "SIGHUP" || o.wait != defaultWait {
		t.Errorf("signal + default wait: %+v %v", o, err)
	}
	if o, err := parseKillOpts([]string{"--wait", "3"}, true); err != nil || o.wait != 3*time.Second || o.sig != 0 {
		t.Errorf("wait 3: %+v %v", o, err)
	}
	for _, bad := range [][]string{
		{"--signal"},
		{"--signal", "--wait"},
		{"--signal", "BOGUS"},
		{"--wait", "soon"},
		{"--wait", "0"},
	} {
		if _, err := parseKillOpts(bad, false); err == nil {
			t.Errorf("%q must fail", bad)
		}
	}
	if _, err := parseKillOpts([]string{"--signal", "HUP"}, true); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("--signal with --force: %v", err)
	}
}

func TestKillOptsVerb(t *testing.T) {
	if v := (killOpts{}).verb(false); v != "SIGTERM" {
		t.Errorf("default: %q", v)
	}
	if v := (killOpts{}).verb(true); v != "SIGKILL" {
		t.Errorf("force: %q", v)
	}
	if v := (killOpts{sig: syscall.SIGHUP, sigName: "SIGHUP"}).verb(false); v != "SIGHUP" {
		t.Errorf("signal: %q", v)
	}
}

// stubSignals records every host signal instead of sending it.
func stubSignals(t *testing.T) *[]syscall.Signal {
	t.Helper()
	old := sendSignal
	t.Cleanup(func() { sendSignal = old })
	var got []syscall.Signal
	sendSignal = func(_ int, sig syscall.Signal) error {
		got = append(got, sig)
		return nil
	}
	return &got
}

func TestStopFuncSendsOneSignal(t *testing.T) {
	sigs := stubSignals(t)
	old := procFile
	t.Cleanup(func() { procFile = old })
	procFile = func(string) ([]byte, error) { return nil, errors.New("no cgroup") }
	stop := killOpts{sig: syscall.SIGUSR1, sigName: "SIGUSR1"}.stopFunc()
	if err := stop(PortItem{PID: "4242"}, false); err != nil || len(*sigs) != 1 || (*sigs)[0] != syscall.SIGUSR1 {
		t.Errorf("host: %v %v", err, *sigs)
	}
	if err := stop(PortItem{PID: "nope"}, false); err == nil {
		t.Error("bad pid must fail")
	}
	if (killOpts{}).stopFunc() == nil {
		t.Error("default stop func missing")
	}
}

func TestSignalItemContainerAndUnit(t *testing.T) {
	log := filepath.Join(t.TempDir(), "argv")
	docker := writeShim(t, "docker", `echo "$@" > `+log+"\n")
	stubContainerEnv(t, docker, "", "", 1000, "")
	if err := signalItem(PortItem{PID: "abc", Source: "docker"}, syscall.SIGHUP, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); strings.TrimSpace(string(b)) != "kill --signal SIGHUP abc" {
		t.Errorf("docker argv: %q", b)
	}

	stubSignals(t)
	stubUnitMain(t, 123, true, nil)
	args := stubSystemctl(t, 0, "")
	if err := signalItem(PortItem{PID: "123"}, syscall.SIGHUP, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(args); strings.TrimSpace(string(b)) != "kill --signal=SIGHUP runner.service" {
		t.Errorf("systemctl argv: %q", b)
	}
}

func TestSelectTargetsAndPortsOf(t *testing.T) {
	items := []PortItem{
		{Port: "3000", Process: "node"},
		{Port: "3000", Process: "node", Proto: "tcp6"},
		{Port: "5432", Process: "docker:db", Source: "docker"},
		{Port: "9000", Process: "go"},
	}
	sel := selectTargets([]string{"9000"}, []string{"db", "node"}, items)
	if len(sel) != 4 || strings.Join(portsOf(sel), ",") != "3000,5432,9000" {
		t.Errorf("sel %+v ports %q", sel, portsOf(sel))
	}
	if got := selectTargets(nil, []string{"vite"}, items); got != nil {
		t.Errorf("no match: %+v", got)
	}
}

// stubScans makes activePorts return each listed scan in turn, repeating the
// last one, and makes sleeping free.
func stubScans(t *testing.T, scans ...[]PortItem) *int {
	t.Helper()
	oldA, oldS := activePorts, sleepFn
	t.Cleanup(func() { activePorts, sleepFn = oldA, oldS })
	calls := 0
	activePorts = func() ([]PortItem, error) {
		i := calls
		if i >= len(scans) {
			i = len(scans) - 1
		}
		calls++
		return scans[i], nil
	}
	sleepFn = func(time.Duration) {}
	return &calls
}

func TestWaitFree(t *testing.T) {
	busy := []PortItem{{Port: "3000"}, {Port: "9999"}}
	stubScans(t, busy, busy, []PortItem{{Port: "9999"}})
	if held := waitFree([]string{"3000"}, time.Second); held != nil {
		t.Errorf("port must free on the third scan: %q", held)
	}
	calls := stubScans(t, busy)
	if held := waitFree([]string{"3000"}, time.Second); strings.Join(held, ",") != "3000" {
		t.Errorf("timeout: %q", held)
	}
	if want := int(time.Second/waitPoll) + 1; *calls != want {
		t.Errorf("scans = %d, want %d", *calls, want)
	}
}

func TestWaitAndReport(t *testing.T) {
	stubScans(t, []PortItem{{Port: "3000"}})
	var ok bool
	out := captureStderr(t, func() { ok = waitAndReport([]string{"3000"}, time.Second) })
	if ok || !strings.Contains(out, "port 3000 still in use after 1s") {
		t.Errorf("ok %v out %q", ok, out)
	}
	stubScans(t, nil)
	if !waitAndReport([]string{"3000"}, time.Second) {
		t.Error("free port must report ok")
	}
}

// stubExit records the exit code cliKill asks for (0 = none).
func stubExit(t *testing.T) *int {
	t.Helper()
	old := exitProcess
	t.Cleanup(func() { exitProcess = old })
	code := 0
	exitProcess = func(c int) { code = c }
	return &code
}

func TestCLIKillByNameWithWait(t *testing.T) {
	withFakeDockerOK(t)
	ctr := PortItem{Port: "8080", PID: "abc", Process: "docker:web", Source: "docker"}
	other := PortItem{Port: "8080", PID: "77", Process: "node"}
	stubScans(t, []PortItem{ctr, other}, nil)
	code := stubExit(t)
	out := captureStdout(t, func() { cliKill([]string{"--kill-name", "web", "--wait", "2"}, false) })
	if *code != 0 || !strings.Contains(out, "stopped docker:web") || strings.Contains(out, "node") {
		t.Errorf("code %d out %q", *code, out)
	}
}

func TestCLIKillWaitTimesOut(t *testing.T) {
	withFakeDockerOK(t)
	stubScans(t, []PortItem{{Port: "8080", PID: "abc", Process: "docker:web", Source: "docker"}})
	code := stubExit(t)
	captureStdout(t, func() { cliKill([]string{"--kill", "8080", "--wait", "1"}, false) })
	if *code != 1 {
		t.Errorf("a port still held after --wait must exit 1, got %d", *code)
	}
}

func TestCLIKillBadOptsAndMissingName(t *testing.T) {
	stubScans(t, []PortItem{{Port: "3000", PID: "1", Process: "node"}})
	code := stubExit(t)
	cliKill([]string{"--kill", "3000", "--signal", "BOGUS"}, false)
	if *code != 2 {
		t.Errorf("bad signal: exit %d, want 2", *code)
	}
	*code = 0
	errOut := captureStderr(t, func() { cliKill([]string{"--kill-name", "vite"}, false) })
	if *code != 1 || !strings.Contains(errOut, "no listener named vite") {
		t.Errorf("missing name: exit %d %q", *code, errOut)
	}
}

func TestCLIKillSignalDryRun(t *testing.T) {
	stubScans(t, []PortItem{{Port: "3000", PID: "42", Process: "node"}})
	stubExit(t)
	out := captureStdout(t, func() { cliKill([]string{"--kill-name", "node", "--signal", "HUP", "--dry-run"}, false) })
	if !strings.Contains(out, "PID 42) via SIGHUP on port 3000") {
		t.Errorf("dry run: %q", out)
	}
}

func TestRunCLIRoutesKillName(t *testing.T) {
	stubScans(t, nil)
	code := stubExit(t)
	captureStderr(t, func() {
		if !runCLI([]string{"--kill-name", "vite"}) {
			t.Error("--kill-name must be handled")
		}
	})
	if *code != 1 || !cliNeedsRoot([]string{"--kill-name", "vite"}) {
		t.Errorf("exit %d", *code)
	}
}

func TestPlainList(t *testing.T) {
	if got := plainRow(PortItem{Proto: "tcp", Addr: "0.0.0.0", Port: "3000", PID: "42", Process: "node", Source: "host"}); got != "tcp\t0.0.0.0\t3000\t42\t-\tnode\thost" {
		t.Errorf("plainRow = %q", got)
	}
	stubActivePorts(t, []PortItem{{Proto: "udp", Port: "53"}})
	out := captureStdout(t, func() { runCLI([]string{"--list", "--plain"}) })
	if out != "udp\t-\t53\t-\t-\t-\t-\n" {
		t.Errorf("--list --plain = %q", out)
	}
}

func TestCompletionsCoverEveryFlag(t *testing.T) {
	for shell, script := range completionScripts {
		for _, f := range cliFlags {
			want := f
			if shell == "fish" {
				want = "-l " + strings.TrimPrefix(f, "--") + " "
			}
			if !strings.Contains(script, want) {
				t.Errorf("%s completion is missing %s", shell, f)
			}
		}
	}
}

// captureStderr runs fn with os.Stderr redirected and returns what it printed.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = old
	return <-done
}
