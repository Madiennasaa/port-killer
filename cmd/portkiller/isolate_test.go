package main

import (
	"os"
	"testing"
)

// TestMain points HOME at a throwaway directory and clears SUDO_USER, so no
// test can append to or replace the developer's real portkiller state (some
// paths, like the guard checks, write events through eventsPath()).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "portkiller-test-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Unsetenv("SUDO_USER")
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
