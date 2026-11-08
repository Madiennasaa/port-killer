package main

import (
	"os"
	"os/user"
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
	chownPaths(paths, uid, gid)
}

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
// component. portkiller runs as root but writes into the invoking user's
// home, where a symlink could otherwise redirect the write to any file.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
}

// writeFileNoFollow is os.WriteFile through openNoFollow.
func writeFileNoFollow(path string, data []byte, perm os.FileMode) error {
	f, err := openNoFollow(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
