package main

import (
	"os"
	"path/filepath"
	"strings"
)

// readGitFile is a seam so gitBranch can be tested against a fake tree.
var readGitFile = os.ReadFile

// parseGitHead (pure) turns .git/HEAD contents into a branch name, or the
// short commit for a detached HEAD. Anything else yields "".
func parseGitHead(b []byte) string {
	s := strings.TrimSpace(string(b))
	if ref, ok := strings.CutPrefix(s, "ref: "); ok {
		return strings.TrimPrefix(ref, "refs/heads/")
	}
	if len(s) >= 40 && strings.Trim(s, "0123456789abcdef") == "" {
		return s[:7]
	}
	return ""
}

// gitDirOf resolves dir/.git: a directory for a normal clone, or a
// "gitdir: PATH" file for worktrees and submodules.
func gitDirOf(dir string) (string, bool) {
	dotgit := filepath.Join(dir, ".git")
	if _, err := readGitFile(filepath.Join(dotgit, "HEAD")); err == nil {
		return dotgit, true
	}
	b, err := readGitFile(dotgit)
	if err != nil {
		return "", false
	}
	gd, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
	if !ok {
		return "", false
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(dir, gd)
	}
	return gd, true
}

// gitBranch walks up from cwd to the nearest repository and returns its
// current branch, or "" outside a repository. Like findProjectRootFrom it
// ignores a repository rooted exactly at home, so a dotfiles repo in ~ does
// not label every process below it.
func gitBranch(cwd, home string) string {
	dir := cwd
	for {
		if home != "" && dir == home {
			return ""
		}
		if gd, ok := gitDirOf(dir); ok {
			b, err := readGitFile(filepath.Join(gd, "HEAD"))
			if err != nil {
				return ""
			}
			return parseGitHead(b)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
