package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// cliNeedsRoot reports whether the given args need root before runCLI runs.
// Listing other users' processes and killing them does; help, version, and
// unknown flags (which exit with an error) do not.
func cliNeedsRoot(args []string) bool {
	for _, a := range args {
		switch a {
		case "--help", "-h", "--version", "-v":
			return false
		}
	}
	for _, a := range args {
		switch a {
		case "--list", "--json", "--kill", "--force":
			return true
		}
	}
	return false
}

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

// hasFlag reports whether args contains the exact flag f.
func hasFlag(args []string, f string) bool {
	for _, a := range args {
		if a == f {
			return true
		}
	}
	return false
}

// runCLI handles non-interactive flags. It returns true if it handled the args.
//
//	portkiller --list [--json]
//	portkiller --kill PORT [--force]
//	portkiller --help
//	portkiller --version
func runCLI(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch {
	case hasFlag(args, "--help") || hasFlag(args, "-h"):
		cliHelp()
	case hasFlag(args, "--version") || hasFlag(args, "-v"):
		cliVersion()
	case hasFlag(args, "--list"):
		cliList(hasFlag(args, "--json"))
	case hasFlag(args, "--kill"):
		cliKill(args, hasFlag(args, "--force"))
	default:
		return rejectUnknownFlag(args)
	}
	return true
}

func cliHelp() {
	fmt.Println("usage: portkiller [--list [--json]] [--kill PORT [--force]] [--version]")
	fmt.Println("\nno flag starts the interactive TUI")
}

func cliVersion() {
	fmt.Printf("portkiller %s\n", version)
}

// rejectUnknownFlag exits 2 on a stray dash-flag; a bare non-flag argument
// means the caller wanted the TUI, so report "not handled" instead.
func rejectUnknownFlag(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			fmt.Fprintf(os.Stderr, "unknown flag %s (try --help)\n", a)
			os.Exit(2)
		}
	}
	return false
}

func cliList(asJSON bool) {
	items := GetActivePorts()
	if asJSON {
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return
	}
	for _, it := range items {
		fmt.Printf("%-4s %-16s %-6s %-10s %-18s %s\n", it.Proto, it.Addr, it.Port, it.PID, it.Project, it.Process)
	}
}

// cliKill stops everything listening on port. Exit codes: 1 = nothing stopped
// or at least one failure, 2 = bad --kill argument.
func cliKill(args []string, force bool) {
	port := ""
	for i, a := range args {
		if a == "--kill" && i+1 < len(args) {
			port = args[i+1]
		}
	}
	if port == "" || strings.HasPrefix(port, "--") {
		fmt.Fprintln(os.Stderr, "--kill needs a port number")
		os.Exit(2)
	}

	seen := map[string]bool{}
	n, failed := 0, false
	for _, it := range GetActivePorts() {
		if it.Port != port || seen[it.PID] {
			continue
		}
		seen[it.PID] = true
		if err := stopItem(it, force); err != nil {
			fmt.Fprintf(os.Stderr, "failed %s (PID %s): %v\n", it.Process, it.PID, err)
			failed = true
			continue
		}
		fmt.Printf("stopped %s (PID %s)\n", it.Process, it.PID)
		n++
	}
	if n == 0 && !failed {
		fmt.Fprintf(os.Stderr, "nothing listening on port %s\n", port)
		os.Exit(1)
	}
	if failed {
		os.Exit(1)
	}
}

// stopItem stops a container by id and everything else by signal.
func stopItem(it PortItem, force bool) error {
	if it.IsDocker() {
		return StopContainer(it.PID)
	}
	return KillProcess(it.PID, force)
}
