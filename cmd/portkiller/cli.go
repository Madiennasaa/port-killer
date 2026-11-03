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
		case "--help", "-h", "--version", "-v", "--completions", "--mcp":
			return false
		}
	}
	for _, a := range args {
		switch a {
		case "--list", "--kill", "--kill-name", "--restart", "--force", "--watch":
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
//	portkiller --list [--json|--plain]
//	portkiller --kill PORT... [--kill-name NAME...] [--force|--signal SIG] [--wait [SECS]] [--dry-run]
//	portkiller --free START-END [--json]
//	portkiller --history [--port N] [--limit N] [--json]
//	portkiller --mcp [--allow-kill]
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
	case hasFlag(args, "--mcp"):
		cliMCP(args)
	case hasFlag(args, "--history"):
		cliHistory(args, hasFlag(args, "--json"))
	case hasFlag(args, "--free"):
		cliFree(args, hasFlag(args, "--json"))
	case hasFlag(args, "--watch"):
		cliWatch(args)
	case hasFlag(args, "--list"):
		cliList(hasFlag(args, "--json"), hasFlag(args, "--plain"))
	case hasFlag(args, "--completions"):
		cliCompletions(args)
	case hasFlag(args, "--restart"):
		cliRestart(args, hasFlag(args, "--force"))
	case hasFlag(args, "--kill") || hasFlag(args, "--kill-name"):
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
	return flagArgs(args, "--kill"), hasFlag(args, "--force")
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
	fmt.Println("usage: portkiller [--list [--json|--plain]] [--kill PORT... [--kill-name NAME...] [--force|--signal SIG] [--wait [SECS]] [--dry-run]] [--restart PORT... [--force] [--wait SECS] [--dry-run]] [--free START-END [--json]] [--completions bash|zsh|fish] [--history [--port N] [--limit N] [--json]] [--mcp [--allow-kill]] [--version]")
	fmt.Println("\nno flag starts the interactive TUI")
	fmt.Println("--kill accepts several ports and shows the impact before stopping")
	fmt.Println("--kill-name stops every listener whose process or container has that name")
	fmt.Println("--signal sends one signal (HUP, USR1, 15...) instead of SIGTERM-then-SIGKILL")
	fmt.Println("--wait waits until the stopped ports are free (default 10s) and fails if they are not")
	fmt.Println("--dry-run prints what --kill would stop without touching anything")
	fmt.Println("--restart stops a listener and starts the same command again once its port is free")
	fmt.Println("--plain prints --list as tab-separated fields for scripts")
	fmt.Println("--free prints the first port that can be bound in the given range")
	fmt.Println("--history prints recent appear/vanished events, newest first")
	fmt.Println("--watch tails port appear/vanish/guard events until Ctrl+C; add --notify for desktop notifications")
	fmt.Println("--mcp serves the Model Context Protocol on stdio for AI agents (read-only")
	fmt.Println("  plus stop previews); add --allow-kill to let the agent stop listeners")
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

func cliList(asJSON, plain bool) {
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
		if plain {
			fmt.Println(plainRow(it))
			continue
		}
		fmt.Printf("%-4s %-16s %-6s %-10s %-18s %s\n", it.Proto, it.Addr, it.Port, it.PID, it.Project, it.Process)
	}
}

// plainRow (pure) is one --list --plain line: tab-separated proto, address,
// port, pid, project, process and source, with "-" for an empty field so
// `cut` and `awk -F'\t'` see a fixed column count.
func plainRow(it PortItem) string {
	f := []string{it.Proto, it.Addr, it.Port, it.PID, it.Project, it.Process, it.Source}
	for i, v := range f {
		if v == "" {
			f[i] = "-"
		}
	}
	return strings.Join(f, "\t")
}

// cliKill stops everything listening on the requested ports or owned by a
// requested process name. Exit codes:
// 1 = nothing stopped, at least one failure, a target with no listener, or a
// port still taken after --wait
// 2 = bad --kill, --signal or --wait argument
func cliKill(args []string, force bool) {
	ports, forceFlag := killTargets(args)
	names := flagArgs(args, "--kill-name")
	if len(ports) == 0 && len(names) == 0 {
		fmt.Fprintln(os.Stderr, "--kill needs a port number (or --kill-name a process name)")
		exitProcess(2)
		return
	}
	// runCLI already decided --force from the same args; keep that decision and
	// note the disagreement so a future arg-parsing change cannot hide it.
	if forceFlag != force {
		fmt.Fprintln(os.Stderr, "warning: --force was given but cliKill was called without it")
	}
	opts, err := parseKillOpts(args, force)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exitProcess(2)
		return
	}

	if hasFlag(args, "--dry-run") {
		dryRunKill(ports, names, opts.verb(force))
		return
	}
	items, err := activePorts()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	sel := selectTargets(ports, names, items)
	ok := stopAndReport(ports, names, force, items, opts.stopFunc())
	if ok && opts.wait > 0 {
		ok = waitAndReport(portsOf(sel), opts.wait)
	}
	if !ok {
		exitProcess(1)
	}
}

// stopAndReport stops the selected listeners and reports the outcome: one
// line per stop and one per port or name with no listener. It returns false
// when nothing stopped, a stop failed, or a target had no listener.
func stopAndReport(ports, names []string, force bool, items []PortItem, stop func(PortItem, bool) error) bool {
	sel := selectTargets(ports, names, items)
	warnSupervised(sel)
	n, failed := killMulti(targetSet(portsOf(sel)), force, sel, stop)
	missing := missingPorts(ports, items)
	for _, p := range missing {
		fmt.Fprintf(os.Stderr, "nothing listening on port %s\n", p)
	}
	missingN := missingKeys(names, items, containerName)
	for _, name := range missingN {
		fmt.Fprintf(os.Stderr, "no listener named %s\n", name)
	}
	return n > 0 && !failed && len(missing) == 0 && len(missingN) == 0
}

// missingPorts (pure) returns the requested ports that have no listener among
// the scanned items.
func missingPorts(ports []string, items []PortItem) []string {
	return missingKeys(ports, items, func(it PortItem) string { return it.Port })
}

// missingKeys (pure) returns the wanted values that key maps no item to.
func missingKeys(want []string, items []PortItem, key func(PortItem) string) []string {
	matched := map[string]bool{}
	for _, it := range items {
		matched[key(it)] = true
	}
	var missing []string
	for _, w := range want {
		if !matched[w] {
			missing = append(missing, w)
		}
	}
	return missing
}

// dryRunKill previews what --kill would stop, one line per listener, without
// touching anything. verb names what a host process would receive. Exit 1
// when no requested target has a listener.
func dryRunKill(ports, names []string, verb string) {
	items, err := activePorts()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	sel := selectTargets(ports, names, items)
	for _, it := range sel {
		if it.IsContainer() {
			fmt.Printf("would stop %s (container %s) via %s stop on port %s\n", it.Process, it.PID, runtimeOf(it.Source), it.Port)
		} else {
			fmt.Printf("would stop %s (PID %s) via %s on port %s\n", it.Process, it.PID, verb, it.Port)
		}
	}
	warnSupervised(sel)
	if len(sel) == 0 {
		fmt.Fprintln(os.Stderr, "no matching listener on the requested ports")
		exitProcess(1)
	}
}

// stopItem stops a container by id and everything else by signal. A host
// process is only signalled when its kernel start time still matches the one
// captured during the scan, so a recycled PID cannot be killed by mistake.
func stopItem(it PortItem, force bool) error {
	if it.IsContainer() {
		return StopContainer(it)
	}
	pid, err := hostPID(it)
	if err != nil {
		return err
	}
	if unit, ok := serviceUnit(pid); ok {
		return systemctl("stop", unit)
	}
	return stopPID(pid, force)
}

// hostPID parses a host item's PID and refuses one whose kernel start time no
// longer matches the scan, so a recycled PID is never signalled.
func hostPID(it PortItem) (int, error) {
	pid, err := strconv.Atoi(it.PID)
	if err != nil {
		return 0, err
	}
	if it.startTime != 0 {
		if cur, ok := readStartTime(pid); ok && cur != it.startTime {
			return 0, fmt.Errorf("pid %d has been reused; refusing to stop it", pid)
		}
	}
	return pid, nil
}
