package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiffPorts(t *testing.T) {
	a := PortItem{Proto: "tcp", Port: "1", PID: "1", Process: "a"}
	b := PortItem{Proto: "tcp", Port: "2", PID: "2", Process: "b"}
	c := PortItem{Proto: "tcp", Port: "3", PID: "3", Process: "c"}
	ev := diffPorts([]PortItem{a, b}, []PortItem{b, c}, time.Unix(0, 0), "x")
	if len(ev) != 2 || ev[0].Kind != "muncul" || ev[0].Port != "3" || ev[1].Kind != "hilang" || ev[1].Cause != "x" {
		t.Fatalf("got %+v", ev)
	}
	if len(diffPorts([]PortItem{a}, []PortItem{a}, time.Now(), "")) != 0 {
		t.Fatal("tanpa perubahan harus kosong")
	}
}

func TestTrimLines(t *testing.T) {
	if got := trimLines("1\n2\n3\n4\n", 2); got != "3\n4\n" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatEvent(t *testing.T) {
	s := formatEvent(Event{Time: time.Date(2026, 1, 1, 19, 42, 0, 0, time.UTC), Kind: "hilang", Proto: "tcp", Port: "5432", Process: "docker:db", Cause: "via portkiller"})
	if !strings.Contains(s, "19:42:00") || !strings.Contains(s, "5432") || !strings.Contains(s, "via portkiller") {
		t.Fatalf("got %q", s)
	}
}

func TestEventsRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "e.jsonl")
	appendEvents(p, []Event{{Time: time.Now(), Kind: "muncul", Port: "1"}, {Time: time.Now(), Kind: "hilang", Port: "2"}})
	got := readEvents(p, 10)
	if len(got) != 2 || got[0].Port != "2" {
		t.Fatalf("got %+v", got)
	}
}
