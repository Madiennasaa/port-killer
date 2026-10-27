package main

import (
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestParseStartTime(t *testing.T) {
	// Field 22 is starttime; fields after the comm start at index 0 (state).
	stat := []byte("4242 (my) weird) S 1 4242 4242 0 -1 4194304 100 0 0 0 1 2 3 4 20 0 1 0 987654 0 0")
	got, ok := parseStartTime(stat)
	if !ok || got != 987654 {
		t.Errorf("parseStartTime = %d, %v; want 987654", got, ok)
	}
	for _, bad := range [][]byte{
		[]byte("no-parens"),
		[]byte("1 (x) S 1 2 3"),
		[]byte("1 (x) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 not-a-number 0 0"),
	} {
		if _, ok := parseStartTime(bad); ok {
			t.Errorf("parseStartTime(%q) must fail", bad)
		}
	}
}

func TestStopPIDGraceful(t *testing.T) {
	oldSignal, oldGone, oldSleep := sendSignal, procGone, sleepFn
	t.Cleanup(func() { sendSignal, procGone, sleepFn = oldSignal, oldGone, oldSleep })

	var sigs []syscall.Signal
	sendSignal = func(_ int, sig syscall.Signal) error {
		sigs = append(sigs, sig)
		return nil
	}
	procGone = func(int) bool { return true }
	sleepFn = func(time.Duration) {}

	if err := stopPID(123, false); err != nil {
		t.Fatalf("stopPID: %v", err)
	}
	if len(sigs) != 1 || sigs[0] != syscall.SIGTERM {
		t.Errorf("signals = %v, want [SIGTERM]", sigs)
	}
}

func TestStopPIDEscalates(t *testing.T) {
	oldSignal, oldGone, oldSleep := sendSignal, procGone, sleepFn
	t.Cleanup(func() { sendSignal, procGone, sleepFn = oldSignal, oldGone, oldSleep })

	var sigs []syscall.Signal
	sendSignal = func(_ int, sig syscall.Signal) error {
		sigs = append(sigs, sig)
		return nil
	}
	procGone = func(int) bool { return false }
	sleepFn = func(time.Duration) {}

	if err := stopPID(123, false); err != nil {
		t.Fatalf("stopPID: %v", err)
	}
	if len(sigs) != 2 || sigs[0] != syscall.SIGTERM || sigs[1] != syscall.SIGKILL {
		t.Errorf("signals = %v, want [SIGTERM SIGKILL]", sigs)
	}
}

func TestStopPIDExitsDuringGrace(t *testing.T) {
	oldSignal, oldGone, oldSleep := sendSignal, procGone, sleepFn
	t.Cleanup(func() { sendSignal, procGone, sleepFn = oldSignal, oldGone, oldSleep })

	var sigs []syscall.Signal
	sendSignal = func(_ int, sig syscall.Signal) error {
		sigs = append(sigs, sig)
		return nil
	}
	calls := 0
	procGone = func(int) bool {
		calls++
		return calls > 2
	}
	sleepFn = func(time.Duration) {}

	if err := stopPID(123, false); err != nil {
		t.Fatalf("stopPID: %v", err)
	}
	if len(sigs) != 1 {
		t.Errorf("signals = %v, want only SIGTERM", sigs)
	}
}

func TestStopPIDForce(t *testing.T) {
	oldSignal := sendSignal
	t.Cleanup(func() { sendSignal = oldSignal })
	var sigs []syscall.Signal
	sendSignal = func(_ int, sig syscall.Signal) error {
		sigs = append(sigs, sig)
		return nil
	}
	if err := stopPID(123, true); err != nil {
		t.Fatalf("stopPID force: %v", err)
	}
	if len(sigs) != 1 || sigs[0] != syscall.SIGKILL {
		t.Errorf("signals = %v, want [SIGKILL]", sigs)
	}
}

func TestStopPIDErrors(t *testing.T) {
	oldSignal, oldGone, oldSleep := sendSignal, procGone, sleepFn
	t.Cleanup(func() { sendSignal, procGone, sleepFn = oldSignal, oldGone, oldSleep })

	sendSignal = func(int, syscall.Signal) error { return errors.New("term denied") }
	if err := stopPID(123, false); err == nil || !strings.Contains(err.Error(), "term denied") {
		t.Errorf("SIGTERM error not surfaced: %v", err)
	}
	if err := stopPID(123, true); err == nil || !strings.Contains(err.Error(), "term denied") {
		t.Errorf("SIGKILL error not surfaced: %v", err)
	}

	sendSignal = func(_ int, sig syscall.Signal) error {
		if sig == syscall.SIGKILL {
			return errors.New("kill denied")
		}
		return nil
	}
	procGone = func(int) bool { return false }
	sleepFn = func(time.Duration) {}
	if err := stopPID(123, false); err == nil || !strings.Contains(err.Error(), "kill denied") {
		t.Errorf("SIGKILL fallback error not surfaced: %v", err)
	}
}

func TestParseStatState(t *testing.T) {
	cases := []struct {
		in    string
		state byte
		ok    bool
	}{
		{"1 (x) S 0", 'S', true},
		{"1 (a b) Z 0", 'Z', true},
		{"broken", 0, false},
		{"1 (x) ", 0, false},
	}
	for _, c := range cases {
		state, ok := parseStatState([]byte(c.in))
		if state != c.state || ok != c.ok {
			t.Errorf("parseStatState(%q) = %q,%v want %q,%v", c.in, state, ok, c.state, c.ok)
		}
	}
}

func TestProcGone(t *testing.T) {
	old := procFile
	t.Cleanup(func() { procFile = old })

	procFile = func(string) ([]byte, error) { return nil, errors.New("gone") }
	if !procGone(1) {
		t.Error("unreadable /proc must count as gone")
	}
	procFile = func(string) ([]byte, error) { return []byte("1 (x) Z 0"), nil }
	if !procGone(1) {
		t.Error("zombie must count as gone")
	}
	procFile = func(string) ([]byte, error) { return []byte("1 (x) S 0"), nil }
	if procGone(1) {
		t.Error("sleeping process is not gone")
	}
}

func TestStopItemRejectsReusedPID(t *testing.T) {
	oldRead := readStartTime
	t.Cleanup(func() { readStartTime = oldRead })
	readStartTime = func(int) (uint64, bool) { return 2, true }

	it := PortItem{PID: "123", Process: "node", startTime: 1}
	err := stopItem(it, false)
	if err == nil || !strings.Contains(err.Error(), "reused") {
		t.Errorf("reused pid must be refused, got %v", err)
	}
}

func TestStopPIDExitsAtFinalCheck(t *testing.T) {
	oldSignal, oldGone, oldSleep := sendSignal, procGone, sleepFn
	t.Cleanup(func() { sendSignal, procGone, sleepFn = oldSignal, oldGone, oldSleep })

	var sigs []syscall.Signal
	sendSignal = func(_ int, sig syscall.Signal) error {
		sigs = append(sigs, sig)
		return nil
	}
	calls := 0
	procGone = func(int) bool {
		calls++
		return calls > int(killGrace/killPollInterval)
	}
	sleepFn = func(time.Duration) {}

	if err := stopPID(123, false); err != nil {
		t.Fatalf("stopPID: %v", err)
	}
	if len(sigs) != 1 {
		t.Errorf("signals = %v, want only SIGTERM", sigs)
	}
}

func TestProcStartTime(t *testing.T) {
	old := procFile
	t.Cleanup(func() { procFile = old })

	procFile = func(string) ([]byte, error) {
		return []byte("1 (x) S " + "0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 42"), nil
	}
	if got, ok := procStartTime(4242); !ok || got != 42 {
		t.Errorf("procStartTime = %d,%v want 42", got, ok)
	}
	procFile = func(string) ([]byte, error) { return []byte("broken"), nil }
	if _, ok := procStartTime(4242); ok {
		t.Error("malformed stat must fail")
	}
	procFile = func(string) ([]byte, error) { return nil, errors.New("gone") }
	if _, ok := procStartTime(4242); ok {
		t.Error("unreadable stat must fail")
	}
}

func TestProcGoneMalformedStat(t *testing.T) {
	old := procFile
	t.Cleanup(func() { procFile = old })
	procFile = func(string) ([]byte, error) { return []byte("garbage"), nil }
	if !procGone(1) {
		t.Fatal("an unparseable stat must count as gone")
	}
}
