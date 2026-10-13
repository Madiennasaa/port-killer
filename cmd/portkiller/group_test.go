package main

import "testing"

func TestGroupByProject(t *testing.T) {
	items := []PortItem{
		{Port: "8000", PID: "1", Process: "php", Project: "simas"},
		{Port: "5173", PID: "2", Process: "node", Project: "simas"},
		{Port: "3000", PID: "3", Process: "node", Project: "app"},
		{Port: "53", PID: "4", Process: "x", Project: "-"},
		{Port: "54", PID: "5", Process: "y", Project: ""},
	}
	g := groupByProject(items)
	if len(g) != 2 || g[0].Project != "app" || g[1].Project != "simas" || len(g[1].Items) != 2 {
		t.Fatalf("got %+v", g)
	}
}

func TestProjectTargets(t *testing.T) {
	g := Group{Project: "p", Items: []PortItem{
		{Port: "8000", PID: "10", Process: "php", Source: "host"},
		{Port: "8001", PID: "10", Process: "php", Source: "host"}, // dedup
		{Port: "22", PID: "11", Process: "sshd", Source: "host"},  // systemProcs
		{Port: "99", PID: "12", Process: "svc", Source: "host"},   // uid 100
		{Port: "5432", PID: "abc", Process: "docker:db", Source: "docker"},
		{Port: "80", PID: "13", Process: "node", Source: "host"}, // uid tak terbaca -> ikut
	}}
	uid := func(pid int) (int, bool) {
		switch pid {
		case 12:
			return 100, true
		case 10:
			return 1000, true
		}
		return 0, false
	}
	got := projectTargets(g, uid)
	if len(got) != 3 || got[0].PID != "10" || got[1].PID != "abc" || got[2].PID != "13" {
		t.Fatalf("got %+v", got)
	}
	h, d := projectSummary(got)
	if h != 2 || d != 1 {
		t.Fatalf("summary %d %d", h, d)
	}
}

func TestProjectNames(t *testing.T) {
	ts := []PortItem{{Process: "a", Port: "1"}, {Process: "b", Port: "2"}, {Process: "c", Port: "3"}}
	if got := projectNames(ts, 2); got != "a(:1), b(:2) +1 lagi" {
		t.Fatalf("got %q", got)
	}
}
