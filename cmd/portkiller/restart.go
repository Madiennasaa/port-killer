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

// userGroupIds is a seam for the supplementary groups of a restart's owner.
var userGroupIds = func(u *user.User) ([]string, error) { return u.GroupIds() }

// credentialGroups (pure) parses group ids for syscall.Credential, always
// including the primary gid. An unparsable entry is skipped rather than
// failing the restart.
func credentialGroups(ids []string, primary uint32) []uint32 {
	out := []uint32{primary}
	for _, id := range ids {
		n, err := strconv.ParseUint(id, 10, 32)
		if err != nil || uint32(n) == primary {
			continue
		}
		out = append(out, uint32(n))
	}
	return out
}

// Restartable holds process data recorded before it is stopped. Exe is the
// absolute /proc/<pid>/exe path when it could be read, so a poisoned PATH
// cannot make a root restart execute an impostor. Env is the process's own
// environment, so dev servers keep their PORT/DATABASE_URL settings.
type Restartable struct {
	Name      string
	PID       string // scan PID (container id for containers), matched against what was stopped
	Cmd       []string
	Cwd       string
	UID       int
	Container bool
	Runtime   string
	CtrUser   string
	ID        string
	Exe       string
	Env       []string
	Unit      string
	Ports     []string // ports it listened on, waited for before relaunch
	Framework string
	// Supervisor names the process manager (pm2, supervisord...) that owns
	// the process. It is recorded before the stop because the ancestry is
	// gone afterwards; such a process must not be relaunched by portkiller,
	// or it would run twice once the manager respawns it.
	Supervisor string
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
	if r.Container {
		return runtimeOf(r.Runtime) + " start " + r.Name
	}
	return strings.Join(r.Cmd, " ")
}

func captureRestart(it PortItem) (Restartable, bool) {
	if it.IsContainer() {
		return Restartable{Name: containerName(it), PID: it.PID, Container: true, Runtime: it.Source, CtrUser: it.ctrUser, ID: it.PID, Ports: []string{it.Port}}, true
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
	r := Restartable{Name: it.Process, PID: it.PID, Cmd: cmd, Cwd: cwd, UID: uid, Ports: []string{it.Port}, Framework: it.Framework}
	r.Exe = capturedExe(it.PID)
	r.Env = capturedEnv(it.PID)
	if unit, ok := serviceUnit(pid); ok {
		r.Unit = unit
	}
	if s, ok := supervisorOf(pid); ok {
		r.Supervisor = s.Name
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
	var out []Restartable
	for _, t := range captureEach(items) {
		if t.ok {
			out = append(out, t.r)
		}
	}
	return out
}

// keepStopped (pure) keeps the captures whose PID was actually stopped, so R
// never relaunches something whose stop failed or was skipped.
func keepStopped(rs []Restartable, stopped []string) []Restartable {
	ok := targetSet(stopped)
	var out []Restartable
	for _, r := range rs {
		if ok[r.PID] {
			out = append(out, r)
		}
	}
	return out
}

// captured pairs a listener with what was recorded to relaunch it; ok is
// false when it cannot be relaunched.
type captured struct {
	it PortItem
	r  Restartable
	ok bool
}

// captureEach captures once per PID/container and gathers every port that
// PID listens on, so a relaunch waits for all of them.
func captureEach(items []PortItem) []captured {
	at := map[string]int{}
	var out []captured
	for _, it := range items {
		if i, ok := at[it.PID]; ok {
			if out[i].ok && !containsArg(out[i].r.Ports, it.Port) {
				out[i].r.Ports = append(out[i].r.Ports, it.Port)
			}
			continue
		}
		at[it.PID] = len(out)
		r, ok := captureRestart(it)
		out = append(out, captured{it, r, ok})
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

// restartLogPath is the per-process log the restarted process writes to, or
// "" when there is no home directory to keep it in.
func restartLogPath(r Restartable) string {
	ev := eventsPath()
	if ev == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(ev), "restart-"+safeFileName(r.Name)+".log")
}

// defaultOpenRestartLog creates the log file, making the state directory on
// first use.
func defaultOpenRestartLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := openNoFollow(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
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
	path := restartLogPath(r)
	if r.Name == "" || path == "" {
		return nil
	}
	f, err := openRestartLog(path)
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
	// Without Groups, Go calls setgroups(0) and the relaunched server would
	// lose memberships such as docker or video.
	ids, _ := userGroupIds(u)
	c.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(r.UID), Gid: uint32(gid), Groups: credentialGroups(ids, uint32(gid))}
	c.Env = withIdentity(c.Env, u.HomeDir, u.Username)
	return nil
}

func restartOne(r Restartable) error {
	switch {
	case r.Unit != "":
		return systemctl("restart", r.Unit)
	case r.Container:
		return containerCmd(runtimeOf(r.Runtime), r.CtrUser, "start", r.ID)
	case len(r.Cmd) == 0:
		return fmt.Errorf("no command line captured for %s", r.Name)
	}
	return restartHost(r)
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
			if err := restartWhenFree(r, restartPortWait); err == nil {
				done++
			} else if first == nil {
				first = err
			}
		}
		return restartMsg{done: done, err: first}
	}
}
