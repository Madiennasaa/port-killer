package main

import (
	"os"
	"os/user"
	"strconv"
)

// effectiveUID is a seam so chownToSudoUser's root path is testable without
// an actual root process.
var effectiveUID = os.Geteuid

func chownToSudoUser(paths ...string) {
	su := os.Getenv("SUDO_USER")
	if effectiveUID() != 0 || su == "" {
		return
	}
	u, err := user.Lookup(su)
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
		if err := os.Chown(p, uid, gid); err != nil && first == nil {
			first = err
		}
	}
	return first
}
