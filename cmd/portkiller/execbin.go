package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// safePath is the trusted PATH used to resolve external binaries. The TUI is
// re-executed with sudo -E, so the caller's PATH must never decide which ss,
// ps, or docker runs as root.
const safePath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// execTimeout bounds every external command so a hung daemon cannot leave the
// TUI stuck in a busy state forever.
const execTimeout = 15 * time.Second

// findInPath resolves name against pathEnv without consulting the process PATH.
// A name containing a separator is returned untouched.
func findInPath(name, pathEnv string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name, nil
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
			continue
		}
		return p, nil
	}
	return "", exec.ErrNotFound
}

// resolveBin returns the absolute path of name on safePath. When it is not
// found the bare name is kept so the command fails loudly instead of silently
// running a user-controlled impostor.
func resolveBin(name string) string {
	if p, err := findInPath(name, safePath); err == nil {
		return p
	}
	return name
}

// External binaries are resolved once from the trusted PATH. Tests may replace
// them to point at shims.
var (
	ssBin        = resolveBin("ss")
	psBin        = resolveBin("ps")
	dockerBin    = resolveBin("docker")
	systemctlBin = resolveBin("systemctl")
)

// hardenedEnv copies the environment but forces PATH to safePath, so children
// cannot pick up an impostor binary from the caller's PATH.
func hardenedEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+safePath)
}

// commandOutput runs bin with a timeout and the hardened PATH.
func commandOutput(bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, bin, args...)
	c.Env = hardenedEnv()
	return c.Output()
}

// commandCombined is commandOutput with stderr folded into the result, for
// docker errors that must be surfaced to the user.
func commandCombined(bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, bin, args...)
	c.Env = hardenedEnv()
	return c.CombinedOutput()
}
