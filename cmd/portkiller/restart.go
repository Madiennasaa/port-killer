package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

// lookupUserById is a seam so restartOne's root-credential path is testable
// without an actual root process.
var lookupUserById = user.LookupId

// Restartable holds process data recorded before it is stopped. Exe is the
// absolute /proc/<pid>/exe path when it could be read, so a poisoned PATH
// cannot make a root restart execute an impostor. Env is the process's own
// environment, so dev servers keep their PORT/DATABASE_URL settings.
type Restartable struct {
	Name   string
	Cmd    []string
	Cwd    string
	UID    int
	Docker bool
	ID     string
	Exe    string
	Env    []string
	Unit   string
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
	if r.Unit != "" {
		return "systemctl restart " + r.Unit
	}
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
	r := Restartable{Name: it.Process, Cmd: cmd, Cwd: cwd, UID: uid}
	r.Exe = capturedExe(it.PID)
	r.Env = capturedEnv(it.PID)
	if unit, ok := serviceUnit(pid); ok {
		r.Unit = unit
	}
	return r, true
}

// capturedExe returns the absolute binary behind pid. A deleted binary is
// skipped: it cannot be executed again.
func capturedExe(pid string) string {
	exe, err := readProcLink("/proc/" + pid + "/exe")
	if err != nil || exe == "" || strings.HasSuffix(exe, " (deleted)") {
		return ""
	}
	return exe
}

// capturedEnv returns the environment pid was started with.
func capturedEnv(pid string) []string {
	b, err := procFile("/proc/" + pid + "/environ")
	if err != nil {
		return nil
	}
	return parseCmdline(b)
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

// safeFileName (pure) turns a process name into a log-file-safe suffix.
func safeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "process"
	}
	return b.String()
}

// restartLogPath is the per-process log the restarted process writes to.
func restartLogPath(r Restartable) string {
	return filepath.Join(filepath.Dir(eventsPath()), "restart-"+safeFileName(r.Name)+".log")
}

// defaultOpenRestartLog creates the log file, making the state directory on
// first use.
func defaultOpenRestartLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	chownToSudoUser(filepath.Dir(path), path)
	return f, nil
}

// openRestartLog is a seam so a log failure can be tested without a read-only
// filesystem.
var openRestartLog = defaultOpenRestartLog

// restartBinary prefers the captured /proc exe over the caller's PATH.
func restartBinary(r Restartable) string {
	if r.Exe != "" {
		return r.Exe
	}
	return r.Cmd[0]
}

// openLogFor attaches a log when the process has a usable name. A logging
// failure must never block the restart.
func openLogFor(r Restartable) *os.File {
	if r.Name == "" {
		return nil
	}
	f, err := openRestartLog(restartLogPath(r))
	if err != nil {
		return nil
	}
	return f
}

func closeLog(f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}

// withIdentity (pure) replaces HOME/USER/LOGNAME so a root restart of another
// user's process does not inherit the caller's identity.
func withIdentity(env []string, home, username string) []string {
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "HOME="), strings.HasPrefix(kv, "USER="), strings.HasPrefix(kv, "LOGNAME="):
			continue
		default:
			out = append(out, kv)
		}
	}
	return append(out, "HOME="+home, "USER="+username, "LOGNAME="+username)
}

// applyRestartCredential drops to the original owner when running as root.
func applyRestartCredential(c *exec.Cmd, r Restartable) error {
	if effectiveUID() != 0 || r.UID == 0 {
		return nil
	}
	u, err := lookupUserById(strconv.Itoa(r.UID))
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return fmt.Errorf("user %s: bad gid %q: %w", u.Username, u.Gid, err)
	}
	c.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(r.UID), Gid: uint32(gid)}
	c.Env = withIdentity(c.Env, u.HomeDir, u.Username)
	return nil
}

func restartOne(r Restartable) error {
	switch {
	case r.Unit != "":
		return systemctl("restart", r.Unit)
	case r.Docker:
		return restartDocker(r)
	case len(r.Cmd) == 0:
		return fmt.Errorf("no command line captured for %s", r.Name)
	}
	return restartHost(r)
}

func restartDocker(r Restartable) error {
	out, err := commandCombined(dockerBin, "start", r.ID)
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func restartHost(r Restartable) error {
	c := exec.Command(restartBinary(r), r.Cmd[1:]...)
	c.Dir = r.Cwd
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	c.Env = r.Env
	if len(c.Env) == 0 {
		c.Env = os.Environ()
	}
	logFile := openLogFor(r)
	if logFile != nil {
		c.Stdout, c.Stderr = logFile, logFile
	}
	if err := applyRestartCredential(c, r); err != nil {
		closeLog(logFile)
		return err
	}
	if err := c.Start(); err != nil {
		closeLog(logFile)
		return err
	}
	go func() {
		_ = c.Wait()
		closeLog(logFile)
	}()
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
