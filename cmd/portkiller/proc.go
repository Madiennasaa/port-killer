package main

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// parseCmdline (pure): splits cmdline bytes on \x00.
func parseCmdline(b []byte) []string {
	s := strings.TrimRight(string(b), "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

// findProjectRootFrom (pure): walks up looking for a marker, stopping at home so
// ~/... without a marker is not labeled with the home name. home="" disables the bound.
// Catatan: marker tepat di $HOME sengaja diabaikan (repo ~/.git, ~/.dotfiles
// are not treated as a project root for cwds below them); cwd==$HOME -> fallback.
func findProjectRootFrom(path string, exists func(string) bool, home string) string {
	markers := []string{".git", "go.mod", "composer.json", "package.json"}
	dir := path
	for {
		atHome := home != "" && dir == home
		if !atHome {
			for _, m := range markers {
				if exists(filepath.Join(dir, m)) {
					return filepath.Base(dir)
				}
			}
		}
		if atHome {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Base(path)
}

// userHomeCache menghindari LookupId (libc/NSS) berulang tiap tick.
// Accessed from tea.Cmd goroutines that may run concurrently, hence the mutex.
var (
	userHomeCache   = map[string]string{}
	userHomeCacheMu sync.Mutex
)

// lookupHomeForUid (pure, lookups injected): SUDO_USER takes priority when uid is 0
// (a sudo dev server has its cwd in the user's home, not /root).
func lookupHomeForUid(uid, sudoUser string, byID func(string) (string, bool), byName func(string) (string, bool)) (string, bool) {
	if uid == "0" && sudoUser != "" {
		if h, ok := byName(sudoUser); ok {
			return h, true
		}
	}
	if uid != "" {
		if h, ok := byID(uid); ok {
			return h, true
		}
	}
	if sudoUser != "" {
		if h, ok := byName(sudoUser); ok {
			return h, true
		}
	}
	return "", false
}

// procUID reads the owning uid of /proc/<pid>; ok is false when the process
// is gone or /proc is unreadable.
func procUID(pid int) (string, bool) {
	fi, err := os.Stat("/proc/" + strconv.Itoa(pid))
	if err != nil {
		return "", false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false
	}
	return strconv.FormatUint(uint64(st.Uid), 10), true
}

// cachedHome returns the cached home for key, if any.
func cachedHome(key string) (string, bool) {
	userHomeCacheMu.Lock()
	defer userHomeCacheMu.Unlock()
	h, ok := userHomeCache[key]
	return h, ok
}

// storeHome remembers a resolved home for key.
func storeHome(key, home string) {
	userHomeCacheMu.Lock()
	defer userHomeCacheMu.Unlock()
	userHomeCache[key] = home
}

// ownerHome returns the home directory of the owner of /proc/<pid>.
// Falls back to SUDO_USER, then $HOME. Does not spawn external processes.
func ownerHome(pid int) string {
	uid, _ := procUID(pid)
	sudoUser := os.Getenv("SUDO_USER")
	cacheKey := "id:" + uid + ":" + sudoUser
	if uid != "" {
		if h, ok := cachedHome(cacheKey); ok {
			return h
		}
	}
	byID := func(id string) (string, bool) {
		u, err := user.LookupId(id)
		if err != nil || u.HomeDir == "" {
			return "", false
		}
		return u.HomeDir, true
	}
	byName := func(name string) (string, bool) {
		u, err := user.Lookup(name)
		if err != nil || u.HomeDir == "" {
			return "", false
		}
		return u.HomeDir, true
	}
	if h, ok := lookupHomeForUid(uid, sudoUser, byID, byName); ok {
		if uid != "" {
			storeHome(cacheKey, h)
		}
		return h
	}
	home, _ := os.UserHomeDir()
	return home
}

// normalizeProjectName (pure): "" / "." / "/" -> "-".
func normalizeProjectName(s string) string {
	if s == "" || s == "." || s == "/" {
		return "-"
	}
	return s
}

func readCwd(pid int) (string, error) {
	return os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
}

// parseStat (pure): extracts ppid and tty_nr from /proc/<pid>/stat.
// Find the last ")" so comm values with spaces/parentheses are safe.
func parseStat(data []byte) (ppid, ttyNr int, err error) {
	s := string(data)
	i := strings.LastIndex(s, ")")
	if i == -1 {
		return 0, 0, strconv.ErrSyntax
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 6 {
		return 0, 0, strconv.ErrSyntax
	}
	// setelah comm: state(0) ppid(1) pgrp(2) session(3) tty_nr(4) ...
	ppid, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, err
	}
	ttyNr, err = strconv.Atoi(strings.TrimSpace(fields[4]))
	if err != nil {
		return 0, 0, err
	}
	return ppid, ttyNr, nil
}

// isSystemProc: name is in systemProcs OR uid is a service account (1..999).
// uid 0 counts as system only if its name is listed; non-system root can be an orphan.
func isSystemProc(name string, uid int) bool {
	if systemProcs[name] {
		return true
	}
	return uid >= 1 && uid <= 999
}

// isOrphan (pure): (ppid==1 or parent is systemd) + no tty + not a system process
// + TCP + belongs to a project (orphaned dev server, not a GUI app/daemon).
func isOrphan(ppid int, parentComm string, ttyNr int, name string, uid int, proto, project string) bool {
	if !strings.HasPrefix(proto, "tcp") {
		return false
	}
	if project == "" || project == "-" {
		return false
	}
	if ppid != 1 && parentComm != "systemd" {
		return false
	}
	if ttyNr != 0 {
		return false
	}
	return !isSystemProc(name, uid)
}

func procUid(pid int) (int, bool) {
	fi, err := os.Stat("/proc/" + strconv.Itoa(pid))
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

func parentCommOf(ppid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(ppid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// enrichOrphan mengisi Orphan; Docker selalu false. Gagal baca /proc -> false.
func enrichOrphan(item *PortItem) {
	if item.IsDocker() {
		item.Orphan = false
		return
	}
	pid, err := strconv.Atoi(item.PID)
	if err != nil {
		item.Orphan = false
		return
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		item.Orphan = false
		return
	}
	ppid, ttyNr, err := parseStat(b)
	if err != nil {
		item.Orphan = false
		return
	}
	uid, _ := procUid(pid)
	item.Orphan = isOrphan(ppid, parentCommOf(ppid), ttyNr, item.Process, uid, item.Proto, item.Project)
}

// enrichProject mengisi Cwd/Project; aman terhadap error /proc.
// If readCwd fails on permissions (e.g. a root process when run without sudo) -> "-".
func enrichProject(item *PortItem) {
	if item.IsDocker() {
		item.Project = strings.TrimPrefix(item.Process, "docker:")
		item.Cwd = "-"
		return
	}
	pid, err := strconv.Atoi(item.PID)
	if err != nil {
		item.Cwd, item.Project = "-", "-"
		return
	}
	cwd, err := readCwd(pid)
	if err != nil || cwd == "" || strings.HasSuffix(cwd, " (deleted)") {
		item.Cwd, item.Project = "-", "-"
		return
	}
	item.Cwd = cwd
	item.Project = findProjectRootFrom(cwd, func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}, ownerHome(pid))
	item.Project = normalizeProjectName(item.Project)
}
