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

// parseCmdline murni: split bytes cmdline dengan \x00.
func parseCmdline(b []byte) []string {
	s := strings.TrimRight(string(b), "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

// findProjectRootFrom murni: walk-up cari marker, berhenti di home agar
// ~/... tanpa marker tidak dilabeli nama home. home="" menonaktifkan batas.
// Catatan: marker tepat di $HOME sengaja diabaikan (repo ~/.git, ~/.dotfiles
// tidak dianggap root proyek untuk cwd di bawahnya); cwd==$HOME -> fallback.
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
// Diakses dari goroutine tea.Cmd yang bisa berjalan bersamaan → mutex.
var (
	userHomeCache   = map[string]string{}
	userHomeCacheMu sync.Mutex
)

// lookupHomeForUid murni-di-inject: prioritas SUDO_USER saat uid 0
// (sudo dev server cwd di home user, bukan /root).
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

// ownerHome mengembalikan home milik uid pemilik /proc/<pid>.
// Jatuh kembali ke SUDO_USER lalu $HOME. Tidak memanggil proses eksternal.
func ownerHome(pid int) string {
	var uid string
	if fi, err := os.Stat("/proc/" + strconv.Itoa(pid)); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			uid = strconv.FormatUint(uint64(st.Uid), 10)
		}
	}
	sudoUser := os.Getenv("SUDO_USER")
	cacheKey := "id:" + uid + ":" + sudoUser
	if uid != "" {
		userHomeCacheMu.Lock()
		h, ok := userHomeCache[cacheKey]
		userHomeCacheMu.Unlock()
		if ok {
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
			userHomeCacheMu.Lock()
			userHomeCache[cacheKey] = h
			userHomeCacheMu.Unlock()
		}
		return h
	}
	home, _ := os.UserHomeDir()
	return home
}

// normalizeProjectName murni: "" / "." / "/" -> "-".
func normalizeProjectName(s string) string {
	if s == "" || s == "." || s == "/" {
		return "-"
	}
	return s
}

func readCwd(pid int) (string, error) {
	return os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
}

// enrichProject mengisi Cwd/Project; aman terhadap error /proc.
// readCwd gagal permission (mis. proses root saat jalan tanpa sudo) -> "-".
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
