package main

import (
	"os"
	"testing"
)

func TestChownToSudoUserNoopWithoutRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: the early-return path does not apply")
	}
	t.Setenv("SUDO_USER", "someone")
	// Must not panic and must not touch ownership.
	chownToSudoUser("/tmp/portkiller-own-test")
}

func TestChownToSudoUserNoopWithoutSudoUser(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	chownToSudoUser("/tmp/portkiller-own-test")
}

func TestChownToSudoUserUnknownUser(t *testing.T) {
	t.Setenv("SUDO_USER", "definitely-not-a-real-user-xyz")
	// Lookup fails -> returns before any chown.
	chownToSudoUser("/tmp/portkiller-own-test")
}
