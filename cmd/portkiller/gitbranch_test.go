package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGitHead(t *testing.T) {
	sha := strings.Repeat("ab12", 10)
	for in, want := range map[string]string{
		"ref: refs/heads/main\n":     "main",
		"ref: refs/heads/feat/login": "feat/login",
		sha + "\n":                   "ab12ab1",
		"garbage":                    "",
		strings.Repeat("z", 40) + "": "",
	} {
		if got := parseGitHead([]byte(in)); got != want {
			t.Errorf("parseGitHead(%q) = %q, want %q", in, got, want)
		}
	}
}

// writeTree creates files (path -> content) under a fresh temp dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestGitBranch(t *testing.T) {
	sha := strings.Repeat("0f", 20)
	root := writeTree(t, map[string]string{
		"repo/.git/HEAD": "ref: refs/heads/main\n",
		"repo/src/app/x": "",
		"wt/.git":        "gitdir: ../store/wt\n",
		"store/wt/HEAD":  sha + "\n",
		"broken/.git":    "gitdir: /nonexistent/portkiller-test\n",
		"repo/odd/.git":  "not a gitdir line\n",
		".git/HEAD":      "ref: refs/heads/dotfiles\n",
		"plain/dir/x":    "",
	})
	// An absolute gitdir needs the temp root, so it is written after the tree.
	if err := os.Mkdir(filepath.Join(root, "abs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "abs/.git"), []byte("gitdir: "+filepath.Join(root, "store/wt")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for cwd, want := range map[string]string{
		"repo/src/app": "main",
		"wt":           sha[:7],
		"abs":          sha[:7],
		"broken":       "",
		"repo/odd":     "main", // a bad .git file is skipped, the parent repo wins
		"plain/dir":    "",     // the repo at home (root) must not count
	} {
		if got := gitBranch(filepath.Join(root, cwd), root); got != want {
			t.Errorf("gitBranch(%s) = %q, want %q", cwd, got, want)
		}
	}
}

func TestGitBranchWalksToRoot(t *testing.T) {
	old := readGitFile
	t.Cleanup(func() { readGitFile = old })
	var seen []string
	readGitFile = func(p string) ([]byte, error) {
		seen = append(seen, p)
		return nil, errors.New("nope")
	}
	if got := gitBranch("/a/b", ""); got != "" {
		t.Errorf("no repo: %q", got)
	}
	if last := seen[len(seen)-1]; last != "/.git" {
		t.Errorf("walk must end at /: %q", seen)
	}
}

func TestFilterMatchesBranch(t *testing.T) {
	m := testModel()
	m.all[1].Branch = "feat/login"
	m.filter = "login"
	m.rebuild()
	if len(m.view) != 1 || m.view[0].Process != "postgres" {
		t.Errorf("branch filter: %+v", m.view)
	}
	if got := detailLines(m.view[0], Detail{Cwd: "/x"})[2]; !strings.Contains(got, "Branch: feat/login") {
		t.Errorf("detail: %q", got)
	}
}
