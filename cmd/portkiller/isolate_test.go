package main

import (
	"os"
	"testing"
)

// TestMain points HOME at a throwaway directory and clears SUDO_USER, so no
// test can append to or replace the developer's real portkiller state (some
// paths, like the guard checks, write events through eventsPath()). Helper
// subprocesses re-exec the test binary and exit inside it, so they reuse the
// parent's directory instead of leaking one each.
func TestMain(m *testing.M) {
	home := os.Getenv("PORTKILLER_TEST_HOME")
	owner := home == ""
	if owner {
		var err error
		if home, err = os.MkdirTemp("", "portkiller-test-home"); err != nil {
			panic(err)
		}
		os.Setenv("PORTKILLER_TEST_HOME", home)
	}
	os.Setenv("HOME", home)
	os.Unsetenv("SUDO_USER")
	code := m.Run()
	if owner {
		os.RemoveAll(home)
	}
	os.Exit(code)
}
