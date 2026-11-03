package main

import (
	"fmt"
	"os"
	"strings"
)

// cliFlags is the one place that knows every CLI flag; the help text and all
// three completion scripts share it so completions cannot drift from reality.
var cliFlags = []string{
	"--help", "--version", "--list", "--json", "--plain", "--kill",
	"--kill-name", "--force", "--signal", "--wait", "--dry-run", "--free", "--watch", "--notify", "--history", "--port",
	"--limit", "--completions",
}

// completionScripts holds the raw completion source per supported shell.
// Handwritten: the flag parser is custom, so no code generator is involved.
var completionScripts = map[string]string{
	"bash": `# bash completion for portkiller
_portkiller() {
	local cur prev
	cur="${COMP_WORDS[COMP_CWORD]}"
	prev="${COMP_WORDS[COMP_CWORD-1]}"
	local flags="--help --version --list --json --plain --kill --kill-name --force --signal --wait --dry-run --free --watch --notify --history --port --limit --completions"
	case "$prev" in
		--completions)
			COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") )
			return
			;;
		--signal)
			COMPREPLY=( $(compgen -W "HUP INT QUIT KILL USR1 USR2 TERM STOP CONT" -- "$cur") )
			return
			;;
		--port|--limit|--wait|--kill-name)
			return
			;;
	esac
	if [[ "$prev" == --kill || "$prev" == --free ]]; then
		# ports and ranges are numeric; nothing to complete
		return
	fi
	COMPREPLY=( $(compgen -W "$flags" -- "$cur") )
}
complete -F _portkiller portkiller
`,
	"zsh": `#compdef portkiller
_portkiller() {
	local -a flags shells
	flags=('--help[show help]' '--version[print version]' '--list[list listeners]'
		'--json[machine-readable output]' '--plain[tab-separated list output]' '--kill[stop listeners by port]'
		'--kill-name[stop listeners by process name]' '--force[use SIGKILL instead of SIGTERM]'
		'--signal[send one signal instead]:signal:(HUP INT QUIT KILL USR1 USR2 TERM STOP CONT)' '--wait[wait until the ports are free]' '--dry-run[preview the kill without stopping]'
		'--free[print the first free port in a range]' '--watch[tail port events until Ctrl+C]' '--notify[notify on watch events]' '--history[show appear/vanished events]'
		'--port[filter history by port]' '--limit[limit history results]' '--completions[print a completion script]')
	shells=(bash zsh fish)
	if [[ ${words[2]} == --completions ]]; then _ describe -t shells shell shells; fi
	_arguments $flags
}
_portkiller "$@"
`,
	"fish": `# fish completion for portkiller
complete -c portkiller -l help -f -d 'show help'
complete -c portkiller -l version -f -d 'print version'
complete -c portkiller -l list -f -d 'list listeners'
complete -c portkiller -l json -f -d 'machine-readable output'
complete -c portkiller -l plain -f -d 'tab-separated list output'
complete -c portkiller -l kill -F -d 'stop listeners by port'
complete -c portkiller -l kill-name -x -d 'stop listeners by process name'
complete -c portkiller -l force -f -d 'use SIGKILL instead of SIGTERM'
complete -c portkiller -l signal -x -a 'HUP INT QUIT KILL USR1 USR2 TERM STOP CONT' -d 'send one signal instead'
complete -c portkiller -l wait -x -d 'wait until the ports are free'
complete -c portkiller -l dry-run -f -d 'preview the kill without stopping'
complete -c portkiller -l free -F -d 'print the first free port in a range'
complete -c portkiller -l watch -f -d 'tail port events until Ctrl+C'
complete -c portkiller -l notify -f -d 'notify on watch events'
complete -c portkiller -l history -f -d 'show appear/vanished events'
complete -c portkiller -l port -F -d 'filter history by port'
complete -c portkiller -l limit -f -a '(seq 1 100)' -d 'limit history results'
complete -c portkiller -l completions -a 'bash zsh fish' -d 'print a completion script'
`,
}

// completionScript returns the completion script for shell, or an error for an
// unsupported shell.
func completionScript(shell string) (string, error) {
	if s, ok := completionScripts[shell]; ok {
		return s, nil
	}
	return "", fmt.Errorf("unknown shell %q (supported: bash, zsh, fish)", shell)
}

// cliCompletions prints the completion script for the shell named right after
// --completions. Exit 2 on a missing or unsupported shell. Like --help, this
// runs without sudo.
func cliCompletions(args []string) {
	shell := ""
	for _, a := range args {
		if a == "--completions" {
			continue
		}
		if strings.HasPrefix(a, "--") {
			continue
		}
		shell = a
		break
	}
	script, err := completionScript(shell)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		exitProcess(2)
		return
	}
	fmt.Print(script)
}
