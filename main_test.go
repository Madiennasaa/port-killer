package main

import (
	"os"
	"strconv"
	"testing"
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
	it := PortItem{Proto: "tcp", Port: "1", PID: strconv.Itoa(os.Getpid()), Process: "portkiller.test", Source: "host"}
	enrichOrphan(&it)
	if it.Orphan {
		t.Fatal("proses test sendiri tidak boleh orphan (punya induk go test)")
	}
}
