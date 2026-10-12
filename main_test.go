package main

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestOrphanItemsDedup(t *testing.T) {
	all := []PortItem{
		{Proto: "tcp", Port: "3000", PID: "42", Process: "node", Orphan: true, Source: "host"},
		{Proto: "udp", Port: "3000", PID: "42", Process: "node", Orphan: true, Source: "host"},
		{Proto: "tcp", Port: "3001", PID: "43", Process: "node", Orphan: true, Source: "host"},
		{Proto: "tcp", Port: "3002", PID: "abc", Process: "docker:web", Orphan: true, Source: "docker"},
		{Proto: "tcp", Port: "3003", PID: "44", Process: "node", Orphan: false, Source: "host"},
	}
	got := orphanItems(all)
	if len(got) != 2 || got[0].PID != "42" || got[1].PID != "43" {
		t.Fatalf("got %+v", got)
	}
}

func TestEnrichOrphanSelf(t *testing.T) {
	if os.Getppid() == 1 {
		t.Skip("induk PID 1 (setsid/CI/container): hasil orphan tidak terdefinisi untuk test ini")
	}
	it := PortItem{Proto: "tcp", Port: "1", PID: strconv.Itoa(os.Getpid()), Process: "portkiller.test", Source: "host", Project: "app"}
	enrichOrphan(&it)
	if it.Orphan {
		t.Fatal("proses test sendiri tidak boleh orphan (punya induk go test)")
	}
}

func TestKillOrphansCmdEndToEnd(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skip("sleep tidak tersedia")
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	targets := orphanItems([]PortItem{
		{Proto: "tcp", Port: "19991", PID: pid, Process: "sleep", Orphan: true, Source: "host"},
		{Proto: "udp", Port: "19991", PID: pid, Process: "sleep", Orphan: true, Source: "host"},
		{Proto: "tcp", Port: "19992", PID: "deadbeef", Process: "docker:web", Orphan: true, Source: "docker"},
	})
	if len(targets) != 1 {
		cmd.Process.Kill()
		t.Fatalf("dedup/filter docker gagal: %+v", targets)
	}
	msg := killOrphansCmd(targets)().(orphansMsg)
	if msg.done != 1 || msg.err != nil {
		cmd.Process.Kill()
		t.Fatalf("got %+v", msg)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done: // mati via SIGTERM dari killOrphansCmd
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		<-done
		t.Fatal("sleep tidak mati setelah SIGTERM")
	}
}
