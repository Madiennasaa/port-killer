package main

import (
	"fmt"
	"strconv"
	"strings"
)

// unitFromCgroup (pure) extracts a systemd system service from a
// /proc/<pid>/cgroup body. Only units under /system.slice/ are returned:
// user-session units cannot be restarted by the root process without a login
// environment, and nested scopes belong to a parent unit.
func unitFromCgroup(data []byte) (string, bool) {
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		comps := strings.Split(strings.Trim(parts[2], "/"), "/")
		for i, c := range comps {
			if c != "system.slice" || i+1 != len(comps)-1 {
				continue
			}
			unit := comps[i+1]
			if strings.HasSuffix(unit, ".service") {
				return unit, true
			}
		}
	}
	return "", false
}

// unitMainPID is a seam: the MainPID systemd records for unit. ok is false
// when systemctl fails or reports no main process.
var unitMainPID = func(unit string) (int, bool) {
	out, err := commandOutput(systemctlBin, "show", "-p", "MainPID", "--value", unit)
	if err != nil {
		return 0, false
	}
	return parseMainPID(out)
}

// parseMainPID (pure) parses `systemctl show -p MainPID --value`; 0 means the
// unit has no main process.
func parseMainPID(out []byte) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// serviceUnit reports the systemd unit owning pid, when pid is that service
// itself: its main process, or a worker running the same binary (nginx,
// postgres). Anything else in the unit's cgroup is a child the service
// spawned (a CI runner's job, a dev server started by a supervisor), and
// stopping the whole unit for it would take down the parent. When systemd
// cannot be asked, the unit is kept so a real service is not signalled behind
// systemd's back.
func serviceUnit(pid int) (string, bool) {
	b, err := procFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return "", false
	}
	unit, ok := unitFromCgroup(b)
	if !ok {
		return "", false
	}
	main, ok := unitMainPID(unit)
	if !ok || main == pid || sameExe(pid, main) {
		return unit, true
	}
	return "", false
}

// sameExe reports whether two pids run the same, readable binary.
func sameExe(a, b int) bool {
	ea, err := readProcLink("/proc/" + strconv.Itoa(a) + "/exe")
	if err != nil {
		return false
	}
	eb, err := readProcLink("/proc/" + strconv.Itoa(b) + "/exe")
	return err == nil && ea == eb
}

// systemctl runs an action against a unit and surfaces systemd's own message on
// failure.
func systemctl(action, unit string) error {
	out, err := commandCombined(systemctlBin, action, unit)
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
