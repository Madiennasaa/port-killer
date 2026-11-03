package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// clockTicks is USER_HZ, the unit of the /proc/<pid>/stat time fields. It is
// 100 on every mainstream Linux build; Go has no portable sysconf for it.
const clockTicks = 100

// procUptimeSec is a seam: the system uptime in seconds from /proc/uptime, or
// 0 when unreadable (cpuPercent then reports 0 instead of a bogus value).
var procUptimeSec = func() float64 {
	b, err := procFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	s, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	return s
}

// parseUsageStat (pure) extracts utime, stime and starttime (in clock ticks)
// from /proc/<pid>/stat. Fields are counted after the last ")" so a comm with
// spaces or parentheses cannot shift them.
func parseUsageStat(data []byte) (utime, stime, start uint64, ok bool) {
	s := string(data)
	i := strings.LastIndex(s, ")")
	if i == -1 {
		return 0, 0, 0, false
	}
	fields := strings.Fields(s[i+1:])
	const utimeIndex, stimeIndex, startTimeIndex = 11, 12, 19 // state is index 0
	if len(fields) <= startTimeIndex {
		return 0, 0, 0, false
	}
	var vals [3]uint64
	for j, idx := range []int{utimeIndex, stimeIndex, startTimeIndex} {
		v, err := strconv.ParseUint(fields[idx], 10, 64)
		if err != nil {
			return 0, 0, 0, false
		}
		vals[j] = v
	}
	return vals[0], vals[1], vals[2], true
}

// parseStatm (pure) returns the resident set size in pages from
// /proc/<pid>/statm (second field).
func parseStatm(data []byte) (uint64, bool) {
	f := strings.Fields(string(data))
	if len(f) < 2 {
		return 0, false
	}
	v, err := strconv.ParseUint(f[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// cpuPercent (pure): average CPU use over the process lifetime, like ps %CPU.
// A non-positive elapsed time (clock drift, a racing exit) yields 0.
func cpuPercent(utime, stime, start uint64, uptime float64) float64 {
	elapsed := uptime - float64(start)/clockTicks
	if elapsed <= 0 {
		return 0
	}
	return float64(utime+stime) / clockTicks / elapsed * 100
}

// cpuText (pure): "4.6%".
func cpuText(pct float64) string { return fmt.Sprintf("%.1f%%", pct) }

// memText (pure): a KB count rendered via formatKB's units.
func memText(kb uint64) string {
	if kb < 1024 {
		return fmt.Sprintf("%d KB", kb)
	}
	return formatKB(strconv.FormatUint(kb, 10))
}

// enrichUsage fills Cpu/Mem from /proc. Docker items and unreadable or gone
// processes get "-" for whichever value could not be read.
func enrichUsage(item *PortItem, uptime float64) {
	item.Cpu, item.Mem = "-", "-"
	if item.IsContainer() {
		return
	}
	if _, err := strconv.Atoi(item.PID); err != nil {
		return
	}
	base := "/proc/" + item.PID
	if b, err := procFile(base + "/stat"); err == nil {
		if ut, st, start, ok := parseUsageStat(b); ok {
			item.Cpu = cpuText(cpuPercent(ut, st, start, uptime))
		}
	}
	if b, err := procFile(base + "/statm"); err == nil {
		if pages, ok := parseStatm(b); ok {
			item.Mem = memText(pages * uint64(os.Getpagesize()) / 1024)
		}
	}
}

// enrichUsages fills Cpu/Mem for a whole scan, reading /proc/uptime once and
// each PID's files once even when it owns several sockets.
func enrichUsages(items []PortItem) {
	uptime := procUptimeSec()
	cache := map[string][2]string{}
	for i := range items {
		if v, ok := cache[items[i].PID]; ok && !items[i].IsContainer() {
			items[i].Cpu, items[i].Mem = v[0], v[1]
			continue
		}
		enrichUsage(&items[i], uptime)
		if !items[i].IsContainer() {
			cache[items[i].PID] = [2]string{items[i].Cpu, items[i].Mem}
		}
	}
}
