package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// desktopUser is the invoking account when portkiller runs as root through
// sudo, and "" otherwise. Commands that belong to the user's session (rootless
// podman, the browser) run as this account instead of root.
func desktopUser() string {
	if su := os.Getenv("SUDO_USER"); effectiveUID() == 0 && su != "" && su != "root" {
		return su
	}
	return ""
}

// asUserArgv builds the command line that runs bin as user. An empty user runs
// bin directly; otherwise it goes through runuser with that user's HOME and
// XDG_RUNTIME_DIR, which rootless podman and desktop helpers need to find
// their storage, socket and session bus.
func asUserArgv(user, bin string, args ...string) (string, []string, error) {
	if user == "" {
		return bin, args, nil
	}
	u, err := lookupUser(user)
	if err != nil {
		return "", nil, err
	}
	argv := append([]string{"-u", user, "--", envBin,
		"HOME=" + u.HomeDir, "XDG_RUNTIME_DIR=/run/user/" + u.Uid, bin}, args...)
	return runuserBin, argv, nil
}

// effectiveUID is a seam so chownToSudoUser's root path is testable without
// an actual root process.
var effectiveUID = os.Geteuid

// lookupUser is a seam so chownToSudoUser's lookup-failure path is testable.
var lookupUser = user.Lookup

func chownToSudoUser(paths ...string) {
	su := os.Getenv("SUDO_USER")
	if effectiveUID() != 0 || su == "" {
		return
	}
	u, err := lookupUser(su)
	if err != nil {
		return
	}
	uid, gid, ok := chownIDs(u.Uid, u.Gid)
	if !ok {
		return
	}
	_ = chownSeam(paths, uid, gid)
}

// chownSeam lets tests see which paths chownToSudoUser hands over.
var chownSeam = chownPaths

// chownIDs (pure): parse a user's uid/gid strings. A parse failure means a
// broken NSS entry, and chowning to 0:0 would be worse than leaving it.
func chownIDs(uidStr, gidStr string) (uid, gid int, ok bool) {
	uid, err := strconv.Atoi(uidStr)
	if err != nil {
		return 0, 0, false
	}
	gid, err = strconv.Atoi(gidStr)
	if err != nil {
		return 0, 0, false
	}
	return uid, gid, true
}

// chownPaths applies chown to each path; the result is aggregated so tests
// can assert ownership without relying on a root process.
func chownPaths(paths []string, uid, gid int) error {
	var first error
	for _, p := range paths {
		// Lchown: a symlink planted in the user's state directory must not
		// hand a root-owned target to the user.
		if err := os.Lchown(p, uid, gid); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// openNoFollow is os.OpenFile that refuses a symlink as the final path
// component, and anything but a regular file with a single link. portkiller
// runs as root but writes into the invoking user's home, where a symlink or a
// hard link could otherwise redirect the write to any file, and a FIFO would
// block it forever (hence O_NONBLOCK). Never pass O_TRUNC: it would apply
// before the checks; replace files with writeFileAtomic instead.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, perm)
	if err != nil {
		return nil, err
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Nlink != 1 {
		f.Close()
		return nil, fmt.Errorf("%s: not a regular single-link file", path)
	}
	return f, nil
}

// maxStateFile caps how much of a state or project file is read.
var maxStateFile = 16 << 20

// readRegular reads at most maxStateFile bytes of path, refusing FIFOs,
// devices and directories, so a planted /dev/zero link or a FIFO can neither
// exhaust memory nor hang a scan. Symlinks are followed: reading through one
// is harmless and dotfile managers rely on them.
func readRegular(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var st syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return nil, fmt.Errorf("%s: not a regular file", path)
	}
	return io.ReadAll(io.LimitReader(f, int64(maxStateFile)))
}

// mkdirAllOwned is os.MkdirAll that hands every directory it creates to the
// sudo user (not just the last one, e.g. a missing ~/.local/state), and
// re-hands dir itself in case an older version left it root-owned.
func mkdirAllOwned(dir string, perm os.FileMode) error {
	var created []string
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil {
			break
		}
		created = append(created, d)
	}
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	chownToSudoUser(append(created, dir)...)
	return nil
}

// writeFileAtomic replaces path with data through a temp file and a rename,
// so readers never see a half-written file and a hard link or symlink at
// path is replaced rather than written through. A non-regular path is
// refused. The temp file is handed to the sudo user before the rename.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	err = errors.Join(werr, f.Chmod(perm), f.Close(), refuseNonRegular(path))
	if err == nil {
		chownToSudoUser(tmp)
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// refuseNonRegular errors when path exists and is not a regular file.
func refuseNonRegular(path string) error {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: refusing to replace a non-regular file", path)
	}
	return nil
}
