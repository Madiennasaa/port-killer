package main

import (
	"strconv"
	"strings"
	"syscall"
	"time"
)

// killGrace bounds how long a SIGTERM is given before escalating to SIGKILL.
const killGrace = 3 * time.Second

const killPollInterval = 100 * time.Millisecond

// Seams so the stop ladder is testable without signalling real processes.
var (
	sendSignal    = syscall.Kill
	sleepFn       = time.Sleep
	readStartTime = procStartTime
)

// parseStartTime (pure) extracts field 22 (starttime) from /proc/<pid>/stat.
// The last ")" is used so comm values with spaces or parentheses are safe.
func parseStartTime(data []byte) (uint64, bool) {
	s := string(data)
	i := strings.LastIndex(s, ")")
	if i == -1 {
		return 0, false
	}
	fields := strings.Fields(s[i+1:])
	const startTimeIndex = 19 // field 22, after comm (state is index 0)
	if len(fields) <= startTimeIndex {
		return 0, false
	}
	v, err := strconv.ParseUint(fields[startTimeIndex], 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseStatState (pure) extracts the state rune from /proc/<pid>/stat.
func parseStatState(data []byte) (byte, bool) {
	s := string(data)
	i := strings.LastIndex(s, ")")
	if i == -1 {
		return 0, false
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) == 0 || len(fields[0]) != 1 {
		return 0, false
	}
	return fields[0][0], true
}

// procStartTime reads a process start time from /proc. ok is false when the
// process is gone or the entry is unreadable.
func procStartTime(pid int) (uint64, bool) {
	b, err := procFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	return parseStartTime(b)
}

// procGone reports whether pid has exited. An unreadable /proc entry counts as
// gone; a zombie (Z) or dead (X) state counts as gone even before its parent
// reaps it. It is a variable so the escalation loop is testable.
var procGone = func(pid int) bool {
	b, err := procFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	state, ok := parseStatState(b)
	if !ok {
		return true
	}
	return state == 'Z' || state == 'X'
}

// stopPID sends SIGTERM, waits up to killGrace, then escalates to SIGKILL.
// force skips the graceful phase.
func stopPID(pid int, force bool) error {
	if force {
		return sendSignal(pid, syscall.SIGKILL)
	}
	if err := sendSignal(pid, syscall.SIGTERM); err != nil {
		return err
	}
	polls := int(killGrace / killPollInterval)
	for i := 0; i < polls; i++ {
		if procGone(pid) {
			return nil
		}
		sleepFn(killPollInterval)
	}
	if procGone(pid) {
		return nil
	}
	return sendSignal(pid, syscall.SIGKILL)
}
