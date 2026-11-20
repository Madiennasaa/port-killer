package main

import (
	"fmt"
	"strconv"
	"strings"
)

// cliModes are the mutually exclusive commands, in the order runCLI tries
// them. --kill and --kill-name are one mode (--kill-name also narrows
// --restart, see cliModeOf).
var cliModes = []string{"--mcp", "--history", "--free", "--watch", "--list", "--completions", "--restart", "--kill"}

// cliModifiers maps each option to the modes it applies to.
var cliModifiers = map[string][]string{
	"--json":       {"--list", "--free", "--history"},
	"--plain":      {"--list"},
	"--force":      {"--kill", "--kill-name", "--restart"},
	"--signal":     {"--kill", "--kill-name"},
	"--wait":       {"--kill", "--kill-name", "--restart"},
	"--dry-run":    {"--kill", "--kill-name", "--restart"},
	"--port":       {"--history", "--watch"},
	"--limit":      {"--history"},
	"--notify":     {"--watch"},
	"--allow-kill": {"--mcp"},
}

// cliArgError (pure) returns why args cannot run, or "" when they can:
// an unknown flag, two modes at once, --json with --plain, or an option
// given without a mode it applies to. --help and --version win over all.
func cliArgError(args []string) string {
	if hasFlag(args, "--help") || hasFlag(args, "-h") || hasFlag(args, "--version") || hasFlag(args, "-v") {
		return ""
	}
	names := map[string]bool{}
	for _, n := range flagArgs(args, "--kill-name") {
		names[n] = true
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") && !names[a] && !knownFlag(a) {
			return fmt.Sprintf("unknown flag %s (try --help)", a)
		}
	}
	var modes []string
	for _, m := range cliModes {
		if hasFlag(args, m) || (m == "--kill" && hasFlag(args, "--kill-name") && !hasFlag(args, "--restart")) {
			modes = append(modes, m)
		}
	}
	if len(modes) > 1 {
		return fmt.Sprintf("%s and %s cannot be combined", modes[0], modes[1])
	}
	if hasFlag(args, "--json") && hasFlag(args, "--plain") {
		return "--json and --plain cannot be combined"
	}
	for _, a := range args {
		allowed, ok := cliModifiers[a]
		if !ok || (len(modes) == 1 && hasAny(allowed, modes[0], args)) {
			continue
		}
		if len(allowed) == 1 {
			return fmt.Sprintf("%s needs %s", a, allowed[0])
		}
		return fmt.Sprintf("%s needs one of %s", a, strings.Join(allowed, ", "))
	}
	return cliValueError(args)
}

// cliValueError (pure) checks option values once the flags themselves are
// valid, so a typo such as "--kill notaport" exits 2 before the sudo prompt
// instead of after the password.
func cliValueError(args []string) string {
	var err error
	switch {
	case hasFlag(args, "--watch"):
		_, err = watchPort(args)
	case hasFlag(args, "--restart"):
		_, err = normalizePorts(flagArgs(args, "--restart"))
	default:
		_, err = normalizePorts(flagArgs(args, "--kill"))
	}
	if err == nil && (hasFlag(args, "--kill") || hasFlag(args, "--kill-name") || hasFlag(args, "--restart")) {
		_, err = parseKillOpts(args, hasFlag(args, "--force"))
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

// hasAny reports whether mode, or --kill-name standing in for --kill, is in
// allowed.
func hasAny(allowed []string, mode string, args []string) bool {
	for _, a := range allowed {
		if a == mode || (a == "--kill-name" && hasFlag(args, a)) {
			return true
		}
	}
	return false
}

// knownFlag reports whether a is a flag portkiller accepts.
func knownFlag(a string) bool {
	switch a {
	case "--help", "-h", "--version", "-v", "--kill-name":
		return true
	}
	for _, m := range cliModes {
		if a == m {
			return true
		}
	}
	_, ok := cliModifiers[a]
	return ok
}

// normalizePorts (pure) validates --kill/--restart ports and strips leading
// zeros, so "08080" matches port 8080 and "abc" is an error, not a silent
// "nothing listening".
func normalizePorts(ports []string) ([]string, error) {
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		n, err := parsePort(p)
		if err != nil {
			return nil, err
		}
		out = append(out, strconv.Itoa(n))
	}
	return out, nil
}
