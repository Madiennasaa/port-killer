package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// cliNeedsRoot reports whether the given args need root before runCLI runs.
// Listing other users' processes and killing them does; help, version, and
// unknown flags (which exit with an error) do not.
func cliNeedsRoot(args []string) bool {
	for _, a := range args {
		switch a {
		case "--help", "-h", "--version", "-v", "--completions":
			return false
		}
	}
	for _, a := range args {
		switch a {
		case "--list", "--kill", "--force", "--watch":
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
//	portkiller --free START-END [--json]
//	portkiller --history [--port N] [--limit N] [--json]
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
	case hasFlag(args, "--history"):
		cliHistory(args, hasFlag(args, "--json"))
	case hasFlag(args, "--free"):
		cliFree(args, hasFlag(args, "--json"))
	case hasFlag(args, "--watch"):
		cliWatch(args)
	case hasFlag(args, "--list"):
		cliList(hasFlag(args, "--json"))
	case hasFlag(args, "--completions"):
		cliCompletions(args)
	case hasFlag(args, "--kill"):
		cliKill(args, hasFlag(args, "--force"))
	default:
		return rejectUnknownFlag(args)
	}
	return true
}

// killTargets (pure): find the --kill targets and whether --force was given.
// Targets are the arguments that directly follow --kill (multiple are allowed);
// a missing or flag-looking argument yields no target.
func killTargets(args []string) (ports []string, force bool) {
	for i, a := range args {
		if a != "--kill" {
			continue
		}
		for _, c := range args[i+1:] {
			if c == "" || strings.HasPrefix(c, "--") {
				break
			}
			ports = append(ports, c)
		}
		return ports, hasFlag(args, "--force")
	}
	return nil, hasFlag(args, "--force")
}

// targetSet maps requested ports for O(1) matching in killMulti.
func targetSet(ports []string) map[string]bool {
	set := make(map[string]bool, len(ports))
	for _, p := range ports {
		set[p] = true
	}
	return set
}

// killLoop runs stopItem over every listener on port, printing each result.
// It returns how many were stopped and whether any failed.
func killLoop(port string, force bool, items []PortItem, stop func(PortItem, bool) error) (n int, failed bool) {
	return killMulti(targetSet([]string{port}), force, items, stop)
}

// killMulti runs stopItem over every listener whose port is in ports, printing
// each result. A PID shared by several matching ports is signalled once: one
// stop frees every port it listens on. It returns how many were stopped and
// whether any failed.
func killMulti(ports map[string]bool, force bool, items []PortItem, stop func(PortItem, bool) error) (n int, failed bool) {
	seen := map[string]bool{}
	for _, it := range items {
		if !ports[it.Port] || seen[it.PID] {
			continue
		}
		seen[it.PID] = true
		if err := stop(it, force); err != nil {
			fmt.Fprintf(os.Stderr, "failed %s (PID %s): %v\n", it.Process, it.PID, err)
			failed = true
			continue
		}
		fmt.Printf("stopped %s (PID %s)\n", it.Process, it.PID)
		n++
	}
	return n, failed
}

func cliHelp() {
	fmt.Println("usage: portkiller [--list [--json]] [--kill PORT... [--force] [--dry-run]] [--free START-END [--json]] [--completions bash|zsh|fish] [--history [--port N] [--limit N] [--json]] [--version]")
	fmt.Println("\nno flag starts the interactive TUI")
	fmt.Println("--kill accepts several ports and shows the impact before stopping")
	fmt.Println("--dry-run prints what --kill would stop without touching anything")
	fmt.Println("--free prints the first port that can be bound in the given range")
	fmt.Println("--history prints recent appear/vanished events, newest first")
	fmt.Println("--watch tails port appear/vanish/guard events until Ctrl+C; add --notify for desktop notifications")
	fmt.Println("--completions prints a completion script; source it or place it")
	fmt.Println("  in your shell's completion directory (fish: ~/.config/fish/completions/portkiller.fish)")
}

func cliVersion() {
	fmt.Printf("portkiller %s\n", version)
}

// exitProcess is os.Exit by default; tests replace it to observe exit paths
// without killing the test binary.
var exitProcess = os.Exit

// activePorts and marshalPorts are seams so the --list/--kill paths are
// testable without depending on the host's listeners or on marshalling.
var (
	activePorts  = scanPorts
	marshalPorts = json.MarshalIndent
)

// rejectUnknownFlag exits 2 on a stray dash-flag; a bare non-flag argument
// means the caller wanted the TUI, so report "not handled" instead.
func rejectUnknownFlag(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			fmt.Fprintf(os.Stderr, "unknown flag %s (try --help)\n", a)
			exitProcess(2)
		}
	}
	return false
}

func cliList(asJSON bool) {
	items, err := activePorts()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	if asJSON {
		b, err := marshalPorts(items, "", "  ")
		if err != nil {
			// PortItem is plain strings and ints: marshalling cannot fail.
			fmt.Fprintln(os.Stderr, "encoding ports failed:", err)
			exitProcess(1)
		}
		fmt.Println(string(b))
		return
	}
	for _, it := range items {
		fmt.Printf("%-4s %-16s %-6s %-10s %-18s %s\n", it.Proto, it.Addr, it.Port, it.PID, it.Project, it.Process)
	}
}

// cliKill stops everything listening on the requested ports. Exit codes:
// 1 = nothing stopped, at least one failure, or a port with no listener
// 2 = bad --kill argument
func cliKill(args []string, force bool) {
	ports, forceFlag := killTargets(args)
	if len(ports) == 0 {
		fmt.Fprintln(os.Stderr, "--kill needs a port number")
		exitProcess(2)
		return
	}
	// runCLI already decided --force from the same args; keep that decision and
	// note the disagreement so a future arg-parsing change cannot hide it.
	if forceFlag != force {
		fmt.Fprintln(os.Stderr, "warning: --force was given but cliKill was called without it")
	}

	if hasFlag(args, "--dry-run") {
		dryRunKill(ports, force)
		return
	}
	items, err := activePorts()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	stopAndReport(ports, force, items)
}

// stopAndReport stops the listeners on the requested ports and reports the
// outcome: one line per stop, one per port with no listener, and exit 1 when
// nothing stopped, a stop failed, or a port had no listener.
func stopAndReport(ports []string, force bool, items []PortItem) {
	n, failed := killMulti(targetSet(ports), force, items, stopItem)
	missing := missingPorts(ports, items)
	for _, p := range missing {
		fmt.Fprintf(os.Stderr, "nothing listening on port %s\n", p)
	}
	if n == 0 || failed || len(missing) > 0 {
		exitProcess(1)
	}
}

// missingPorts (pure) returns the requested ports that have no listener among
// the scanned items.
func missingPorts(ports []string, items []PortItem) []string {
	matched := map[string]bool{}
	for _, it := range items {
		matched[it.Port] = true
	}
	var missing []string
	for _, p := range ports {
		if !matched[p] {
			missing = append(missing, p)
		}
	}
	return missing
}

// dryRunKill previews what --kill would stop, one line per listener, without
// touching anything. Exit 1 when no requested port has a listener.
func dryRunKill(ports []string, force bool) {
	set := targetSet(ports)
	items, err := activePorts()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	verb := "SIGTERM"
	if force {
		verb = "SIGKILL"
	}
	matched := 0
	for _, it := range items {
		if !set[it.Port] {
			continue
		}
		if it.IsDocker() {
			fmt.Printf("would stop %s (container %s) via docker stop on port %s\n", it.Process, it.PID, it.Port)
		} else {
			fmt.Printf("would stop %s (PID %s) via %s on port %s\n", it.Process, it.PID, verb, it.Port)
		}
		matched++
	}
	if matched == 0 {
		fmt.Fprintln(os.Stderr, "no matching listener on the requested ports")
		exitProcess(1)
	}
}

// stopItem stops a container by id and everything else by signal. A host
// process is only signalled when its kernel start time still matches the one
// captured during the scan, so a recycled PID cannot be killed by mistake.
func stopItem(it PortItem, force bool) error {
	if it.IsDocker() {
		return StopContainer(it.PID)
	}
	pid, err := strconv.Atoi(it.PID)
	if err != nil {
		return err
	}
	if it.startTime != 0 {
		if cur, ok := readStartTime(pid); ok && cur != it.startTime {
			return fmt.Errorf("pid %d has been reused; refusing to stop it", pid)
		}
	}
	if unit, ok := serviceUnit(pid); ok {
		return systemctl("stop", unit)
	}
	return stopPID(pid, force)
}
