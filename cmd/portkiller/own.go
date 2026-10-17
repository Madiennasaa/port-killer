package main

import (
	"os"
	"os/user"
	"strconv"
)

func chownToSudoUser(paths ...string) {
	su := os.Getenv("SUDO_USER")
	if os.Geteuid() != 0 || su == "" {
		return
	}
	u, err := user.Lookup(su)
	if err != nil {
		return
	}
	// user.Uid/Gid are always decimal strings on Unix; a parse failure means
	// a broken NSS entry, and chowning to 0:0 would be worse than leaving it.
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return
	}
	for _, p := range paths {
		os.Chown(p, uid, gid)
	}
}
