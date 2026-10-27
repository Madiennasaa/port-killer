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

// serviceUnit reports the systemd unit owning pid, when it is a plain system
// service.
func serviceUnit(pid int) (string, bool) {
	b, err := procFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return "", false
	}
	return unitFromCgroup(b)
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
