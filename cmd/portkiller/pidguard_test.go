package main

import (
	"os"
	"strconv"
	"syscall"
	"testing"
)

// TestNeverSignalInitOrSelf: PID 0 (the caller's process group), negative
// PIDs (every process), PID 1 (init; systemd socket activation shows it as a
// listener) and portkiller itself are refused on every stop path.
func TestNeverSignalInitOrSelf(t *testing.T) {
	oldSig := sendSignal
	t.Cleanup(func() { sendSignal = oldSig })
	sent := 0
	sendSignal = func(int, syscall.Signal) error { sent++; return nil }

	for _, pid := range []string{"0", "1", "-1", "-42", strconv.Itoa(os.Getpid())} {
		it := PortItem{Proto: "tcp", Port: "22", PID: pid, Process: "systemd", Source: "host"}
		if err := stopItem(it, false); err == nil {
			t.Errorf("stopItem(pid %s) succeeded", pid)
		}
		if err := stopItem(it, true); err == nil {
			t.Errorf("stopItem(pid %s, force) succeeded", pid)
		}
		if err := signalItem(it, syscall.SIGHUP, "SIGHUP"); err == nil {
			t.Errorf("signalItem(pid %s) succeeded", pid)
		}
		if err := KillProcess(pid, true); err == nil {
			t.Errorf("KillProcess(%s) succeeded", pid)
		}
	}
	if sent != 0 {
		t.Fatalf("%d signals were sent to protected pids", sent)
	}
}
