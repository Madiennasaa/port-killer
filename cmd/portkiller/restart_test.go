package main

import (
	"os"
	"strconv"
	"testing"
)

func TestRestartable(t *testing.T) {
	cases := []struct {
		cmd  []string
		cwd  string
		want bool
	}{
		{[]string{"node", "x.js"}, "/srv/app", true},
		{nil, "/srv/app", false},
		{[]string{"node"}, "", false},
		{[]string{"node"}, "-", false},
		{[]string{"node"}, "/gone (deleted)", false},
	}
	for i, c := range cases {
		if got := restartable(c.cmd, c.cwd); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
}

func TestRestartDesc(t *testing.T) {
	if restartDesc(Restartable{Cmd: []string{"python3", "-m", "http.server"}}) != "python3 -m http.server" {
		t.Fatal("host")
	}
	if restartDesc(Restartable{Docker: true, Name: "db"}) != "docker start db" {
		t.Fatal("docker")
	}
}

func TestCaptureAllDedup(t *testing.T) {
	items := []PortItem{
		{PID: "abc", Process: "docker:db", Source: "docker", Port: "5432"},
		{PID: "abc", Process: "docker:db", Source: "docker", Port: "5433"},
		{PID: "zzz", Process: "x", Source: "host"},
	}
	got := captureAll(items)
	if len(got) != 1 || !got[0].Docker || got[0].Name != "db" {
		t.Fatalf("got %+v", got)
	}
}

func TestCaptureSelf(t *testing.T) {
	r, ok := captureRestart(PortItem{PID: strconv.Itoa(os.Getpid()), Process: "self", Source: "host"})
	if !ok || len(r.Cmd) == 0 || r.Cwd == "" {
		t.Fatalf("got %+v ok=%v", r, ok)
	}
}
