package main

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestParseStatus(t *testing.T) {
	st := parseStatus([]byte("Name:\tpython3\nUid:\t1000\t1000\t1000\t1000\nPPid:\t1\nVmRSS:\t 20480 kB\nbad line\n"))
	if st["Name"] != "python3" || firstField(st["Uid"]) != "1000" || st["PPid"] != "1" || st["VmRSS"] != "20480 kB" {
		t.Fatalf("got %+v", st)
	}
}

func TestFormatKB(t *testing.T) {
	for in, want := range map[string]string{"": "-", "x": "-", "512 kB": "512 kB", "20480 kB": "20.0 MB", "2097152 kB": "2.0 GB"} {
		if got := formatKB(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestDetailLinesHeight(t *testing.T) {
	l := detailLines(PortItem{Process: "x", Port: "1", PID: "2"}, Detail{})
	if len(l) != detailHeight {
		t.Fatalf("got %d", len(l))
	}
}

func TestReadDetailDocker(t *testing.T) {
	d := readDetail(PortItem{Process: "docker:web", PID: "abc", Source: "docker"})
	if !strings.Contains(d.Cmdline, "web") || d.Cwd != "-" {
		t.Fatalf("got %+v", d)
	}
}

func TestReadDetailSelf(t *testing.T) {
	d := readDetail(PortItem{Process: "x", PID: strconv.Itoa(os.Getpid()), Source: "host"})
	if d.Cwd == "-" || d.Cmdline == "-" || d.Parent == "-" || d.Threads == "-" {
		t.Fatalf("got %+v", d)
	}
}
