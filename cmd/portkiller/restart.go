package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

// lookupUserById is a seam so restartOne's root-credential path is testable
// without an actual root process.
var lookupUserById = user.LookupId

// Restartable holds process data recorded before it is stopped.
type Restartable struct {
	Name   string
	Cmd    []string
	Cwd    string
	UID    int
	Docker bool
	ID     string
}

type restartMsg struct {
	done int
	err  error
}

// restartable (pure): is there enough data to relaunch?
func restartable(cmd []string, cwd string) bool {
	return len(cmd) > 0 && cmd[0] != "" && cwd != "" && cwd != "-" && !strings.HasSuffix(cwd, " (deleted)")
}

// restartDesc (pure).
func restartDesc(r Restartable) string {
	if r.Docker {
		return "docker start " + r.Name
	}
	return strings.Join(r.Cmd, " ")
}

func captureRestart(it PortItem) (Restartable, bool) {
	if it.IsDocker() {
		return Restartable{Name: strings.TrimPrefix(it.Process, "docker:"), Docker: true, ID: it.PID}, true
	}
	pid, err := strconv.Atoi(it.PID)
	if err != nil {
		return Restartable{}, false
	}
	b, err := os.ReadFile("/proc/" + it.PID + "/cmdline")
	if err != nil {
		return Restartable{}, false
	}
	cmd := parseCmdline(b)
	cwd, err := readCwd(pid)
	if err != nil || !restartable(cmd, cwd) {
		return Restartable{}, false
	}
	uid, ok := procUid(pid)
	if !ok {
		// /proc vanished between reads: the process is already gone.
		return Restartable{}, false
	}
	return Restartable{Name: it.Process, Cmd: cmd, Cwd: cwd, UID: uid}, true
}

// captureAll: unik per PID/container, dipanggil sebelum kill.
func captureAll(items []PortItem) []Restartable {
	seen := map[string]bool{}
	var out []Restartable
	for _, it := range items {
		if seen[it.PID] {
			continue
		}
		seen[it.PID] = true
		if r, ok := captureRestart(it); ok {
			out = append(out, r)
		}
	}
	return out
}

func restartOne(r Restartable) error {
	if r.Docker {
		out, err := exec.Command("docker", "start", r.ID).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	c := exec.Command(r.Cmd[0], r.Cmd[1:]...)
	c.Dir = r.Cwd
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if effectiveUID() == 0 && r.UID != 0 {
		u, err := lookupUserById(strconv.Itoa(r.UID))
		if err != nil {
			return err
		}
		gid, err := strconv.Atoi(u.Gid)
		if err != nil {
			return fmt.Errorf("user %s: bad gid %q: %w", u.Username, u.Gid, err)
		}
		c.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(r.UID), Gid: uint32(gid)}
		c.Env = append(os.Environ(), "HOME="+u.HomeDir, "USER="+u.Username, "LOGNAME="+u.Username)
	}
	if err := c.Start(); err != nil {
		return err
	}
	go c.Wait()
	return nil
}

func restartCmd(rs []Restartable) tea.Cmd {
	snap := append([]Restartable(nil), rs...)
	return func() tea.Msg {
		done := 0
		var first error
		for _, r := range snap {
			if err := restartOne(r); err == nil {
				done++
			} else if first == nil {
				first = err
			}
		}
		return restartMsg{done: done, err: first}
	}
}
