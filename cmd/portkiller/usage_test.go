package main

import (
	"errors"
	"strings"
	"testing"
)

var errGone = errors.New("gone")

func TestParseUsageStat(t *testing.T) {
	// after "comm)": state ppid pgrp session tty tpgid flags minflt cminflt
	// majflt cmajflt utime stime cutime cstime priority nice threads itreal
	// starttime
	b := []byte("1234 (node pr) S 1 2 3 4 5 6 7 8 9 10 250 100 6 7 60 61 62 63 12345")
	utime, stime, start, ok := parseUsageStat(b)
	if !ok || utime != 250 || stime != 100 || start != 12345 {
		t.Errorf("parseUsageStat = %d, %d, %d, %v", utime, stime, start, ok)
	}
	if _, _, _, ok := parseUsageStat([]byte("garbage")); ok {
		t.Error("malformed stat must fail")
	}
}

func TestParseStatm(t *testing.T) {
	pages, ok := parseStatm([]byte("100 50 20 5 0 10 0"))
	if !ok || pages != 50 {
		t.Errorf("parseStatm = %d, %v", pages, ok)
	}
	if _, ok := parseStatm([]byte("garbage")); ok {
		t.Error("malformed statm must fail")
	}
}

func TestCpuPercent(t *testing.T) {
	// 350 ticks = 3.5 s of CPU since 12345 ticks after boot; uptime 200 s.
	got := cpuPercent(250, 100, 12345, 200)
	want := 3.5 / (200 - 123.45) * 100
	if got < want-0.001 || got > want+0.001 {
		t.Errorf("cpuPercent = %f, want %f", got, want)
	}
	// Impossible elapsed time (clock drift, dying process) stays at 0.
	if got := cpuPercent(250, 100, 999999, 10); got != 0 {
		t.Errorf("cpuPercent with negative elapsed = %f, want 0", got)
	}
}

func TestCpuMemText(t *testing.T) {
	if got := cpuText(4.57); got != "4.6%" {
		t.Errorf("cpuText = %q", got)
	}
	if got := memText(512); got != "512 KB" {
		t.Errorf("memText small = %q", got)
	}
	if got := memText(2048); got != "2.0 MB" {
		t.Errorf("memText large = %q", got)
	}
}

func TestEnrichUsageHost(t *testing.T) {
	oldStat, oldUptime := procFile, procUptimeSec
	procUptimeSec = func() float64 { return 200 }
	procFile = func(path string) ([]byte, error) {
		switch path {
		case "/proc/42/stat":
			return []byte("42 (node) S 1 2 3 4 5 6 7 8 9 10 250 100 6 7 60 61 62 63 12345"), nil
		case "/proc/42/statm":
			return []byte("100 2048 20 5 0 10 0"), nil
		default:
			return nil, errGone
		}
	}
	t.Cleanup(func() { procFile, procUptimeSec = oldStat, oldUptime })
	it := PortItem{PID: "42", Process: "node"}
	enrichUsage(&it, procUptimeSec())
	if it.Cpu == "" || it.Mem == "" || it.Cpu == "-" {
		t.Errorf("cpu/mem not filled: %q %q", it.Cpu, it.Mem)
	}
}

func TestEnrichUsageDockerAndGone(t *testing.T) {
	it := PortItem{PID: "abc123", Process: "web", Source: "docker"}
	enrichUsage(&it, 100)
	if it.Cpu != "-" || it.Mem != "-" {
		t.Errorf("docker usage = %q %q, want dashes", it.Cpu, it.Mem)
	}
	oldStat := procFile
	procFile = func(string) ([]byte, error) { return nil, errGone }
	t.Cleanup(func() { procFile = oldStat })
	it2 := PortItem{PID: "99999"}
	enrichUsage(&it2, 100)
	if it2.Cpu != "-" || it2.Mem != "-" {
		t.Errorf("gone process usage = %q %q, want dashes", it2.Cpu, it2.Mem)
	}
}

func TestEnrichUsagesBatch(t *testing.T) {
	oldStat, oldUptime := procFile, procUptimeSec
	procUptimeSec = func() float64 { return 200 }
	procFile = func(path string) ([]byte, error) {
		if path == "/proc/7/stat" {
			return []byte("7 (go) S 1 2 3 4 5 6 7 8 9 10 100 0 0 0 60 61 62 63 100"), nil
		}
		return nil, errGone
	}
	t.Cleanup(func() { procFile, procUptimeSec = oldStat, oldUptime })
	items := []PortItem{
		{PID: "7", Process: "go"},
		{PID: "deadpool", Process: "x"},
	}
	enrichUsages(items)
	if items[0].Cpu == "" || items[0].Mem == "" {
		t.Errorf("item 0: cpu=%q mem=%q", items[0].Cpu, items[0].Mem)
	}
	if items[1].Cpu != "-" || items[1].Mem != "-" {
		t.Errorf("item 1: %q %q, want dashes", items[1].Cpu, items[1].Mem)
	}
}

func TestDetailLinesShowCPU(t *testing.T) {
	d := Detail{Mem: "1.0 MB", Threads: "4"}
	if l := detailLines(PortItem{Cpu: "2.5%"}, d)[4]; !strings.Contains(l, "CPU: 2.5%") {
		t.Errorf("detail line = %q, want CPU: 2.5%%", l)
	}
	if l := detailLines(PortItem{}, d)[4]; !strings.Contains(l, "CPU: -") {
		t.Errorf("unenriched detail line = %q, want CPU: -", l)
	}
}
