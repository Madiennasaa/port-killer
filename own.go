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
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	for _, p := range paths {
		os.Chown(p, uid, gid)
	}
}
