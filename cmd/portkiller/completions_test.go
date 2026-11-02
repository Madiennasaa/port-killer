package main

import (
	"strings"
	"testing"
)

func TestCompletionScriptPerShell(t *testing.T) {
	cases := []struct {
		shell string
		mark  string
		flag  string
	}{
		{"bash", "complete -F _portkiller portkiller", "--kill"},
		{"zsh", "#compdef portkiller", "--kill"},
		{"fish", "complete -c portkiller", "-l kill"},
	}
	for _, c := range cases {
		script, err := completionScript(c.shell)
		if err != nil {
			t.Errorf("completionScript(%q): %v", c.shell, err)
			continue
		}
		if !strings.Contains(script, c.mark) {
			t.Errorf("completionScript(%q) missing %q:\n%.200s", c.shell, c.mark, script)
		}
		if !strings.Contains(script, c.flag) {
			t.Errorf("completionScript(%q) missing flag %q", c.shell, c.flag)
		}
	}
}

func TestCompletionScriptUnknownShell(t *testing.T) {
	if _, err := completionScript("tcsh"); err == nil {
		t.Error("unknown shell must error")
	}
}

func TestRunCLICompletionsHandled(t *testing.T) {
	if !runCLI([]string{"--completions", "bash"}) {
		t.Fatal("--completions bash should be handled")
	}
}

func TestCLINeedsRootCompletionsIsFree(t *testing.T) {
	if cliNeedsRoot([]string{"--completions", "bash"}) {
		t.Error("--completions must run without sudo")
	}
}

func TestCompletionsInvalidShellExitsTwo(t *testing.T) {
	if cliNeedsRoot([]string{"--completions", "tcsh"}) {
		t.Error("--completions must run without sudo")
	}
	oldExit := exitProcess
	var code int
	exitProcess = func(c int) { code = c }
	t.Cleanup(func() { exitProcess = oldExit })
	cliCompletions([]string{"--completions", "tcsh"})
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}
