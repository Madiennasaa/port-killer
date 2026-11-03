package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// restartPortWait bounds how long the TUI's R waits for the old listener to
// let go of its ports before relaunching.
const restartPortWait = 5 * time.Second

// restartWhenFree relaunches r once its ports have no listener, so a dev
// server is not started into an EADDRINUSE crash while the old one is still
// shutting down. systemd and container runtimes handle the handover
// themselves, so only host processes wait.
func restartWhenFree(r Restartable, timeout time.Duration) error {
	if r.Unit == "" && !r.Container && len(r.Ports) > 0 {
		if held := waitFree(r.Ports, timeout); len(held) > 0 {
			return fmt.Errorf("port %s still in use after %s; %s not restarted", strings.Join(held, ","), timeout, r.Name)
		}
	}
	return restartOne(r)
}

// restartLabel (pure) names what is restarted: the framework when known.
func restartLabel(r Restartable) string {
	if r.Framework != "" {
		return r.Framework + " (" + r.Name + ")"
	}
	return r.Name
}

// restartOutcome is one relaunch in --restart output and the MCP result.
type restartOutcome struct {
	Process   string   `json:"process"`
	Framework string   `json:"framework,omitempty"`
	Ports     []string `json:"ports"`
	Command   string   `json:"command"`
	Status    string   `json:"status"` // would-restart | restarted | failed
	Error     string   `json:"error,omitempty"`
}

// restartTargets stops each selected listener and starts it again with the
// command line, directory and environment it had. It refuses before stopping
// anything when one target could not come back: its command line is
// unreadable, or a process manager owns it (relaunching would run it twice).
func restartTargets(sel []PortItem, force bool, stop func(PortItem, bool) error, timeout time.Duration, dryRun bool) ([]restartOutcome, error) {
	targets := captureEach(sel)
	var refused []string
	for _, t := range targets {
		if !t.ok {
			refused = append(refused, fmt.Sprintf("%s (PID %s): command line or working directory unreadable", t.it.Process, t.it.PID))
			continue
		}
		if pid, err := strconv.Atoi(t.it.PID); err == nil && !t.r.Container {
			if s, ok := supervisorOf(pid); ok {
				refused = append(refused, fmt.Sprintf("%s (PID %s): managed by %s, restart it there", t.it.Process, t.it.PID, s.Name))
			}
		}
	}
	if len(refused) > 0 {
		return nil, errors.New("cannot restart " + strings.Join(refused, "; ") + " (nothing was stopped)")
	}
	out := make([]restartOutcome, 0, len(targets))
	for _, t := range targets {
		o := restartOutcome{Process: t.it.Process, Framework: t.r.Framework, Ports: t.r.Ports, Command: restartDesc(t.r), Status: "would-restart"}
		if !dryRun {
			o.Status = "restarted"
			if err := stop(t.it, force); err != nil {
				o.Status, o.Error = "failed", "stop: "+err.Error()
			} else if err := restartWhenFree(t.r, timeout); err != nil {
				o.Status, o.Error = "failed", err.Error()
			}
		}
		out = append(out, o)
	}
	return out, nil
}

// cliRestart implements --restart PORT...: stop, wait for the port, relaunch.
func cliRestart(args []string, force bool) {
	ports := flagArgs(args, "--restart")
	names := flagArgs(args, "--kill-name")
	if len(ports) == 0 && len(names) == 0 {
		fmt.Fprintln(os.Stderr, "--restart needs a port number (or --kill-name a process name)")
		exitProcess(2)
		return
	}
	opts, err := parseKillOpts(args, force)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exitProcess(2)
		return
	}
	timeout := opts.wait
	if timeout == 0 {
		timeout = defaultWait
	}
	items, err := activePorts()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	ok := true
	for _, p := range missingPorts(ports, items) {
		fmt.Fprintf(os.Stderr, "nothing listening on port %s\n", p)
		ok = false
	}
	for _, name := range missingKeys(names, items, containerName) {
		fmt.Fprintf(os.Stderr, "no listener named %s\n", name)
		ok = false
	}
	res, err := restartTargets(selectTargets(ports, names, items), force, opts.stopFunc(), timeout, hasFlag(args, "--dry-run"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exitProcess(1)
		return
	}
	for _, o := range res {
		label := restartLabel(Restartable{Name: o.Process, Framework: o.Framework})
		switch o.Status {
		case "failed":
			fmt.Fprintf(os.Stderr, "failed to restart %s: %s\n", label, o.Error)
			ok = false
		case "would-restart":
			fmt.Printf("would restart %s on :%s: %s\n", label, strings.Join(o.Ports, ",:"), o.Command)
		default:
			fmt.Printf("restarted %s on :%s: %s\n", label, strings.Join(o.Ports, ",:"), o.Command)
		}
	}
	if len(res) == 0 {
		ok = false
	}
	if !ok {
		exitProcess(1)
	}
}
