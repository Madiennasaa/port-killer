package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// defaultWait is how long --wait waits for the ports to free up when no
// seconds value follows it.
const defaultWait = 10 * time.Second

// waitPoll is how often --wait rescans the listeners.
const waitPoll = 200 * time.Millisecond

// signalNames maps the signals worth sending to a server by hand. Anything
// else can still be given by number.
var signalNames = map[string]syscall.Signal{
	"HUP": syscall.SIGHUP, "INT": syscall.SIGINT, "QUIT": syscall.SIGQUIT,
	"KILL": syscall.SIGKILL, "USR1": syscall.SIGUSR1, "USR2": syscall.SIGUSR2,
	"TERM": syscall.SIGTERM, "STOP": syscall.SIGSTOP, "CONT": syscall.SIGCONT,
}

// flagArgs (pure) returns the arguments that directly follow flag, up to the
// next "--" flag or empty argument.
func flagArgs(args []string, flag string) []string {
	var vals []string
	for i, a := range args {
		if a != flag {
			continue
		}
		for _, c := range args[i+1:] {
			if c == "" || strings.HasPrefix(c, "--") {
				break
			}
			vals = append(vals, c)
		}
		return vals
	}
	return nil
}

// parseSignal (pure) accepts HUP, SIGHUP, hup or a number from 1 to 64 and
// returns the signal plus the name to hand to docker/podman/systemctl.
func parseSignal(s string) (syscall.Signal, string, error) {
	if n, err := strconv.Atoi(s); err == nil {
		if n < 1 || n > 64 {
			return 0, "", fmt.Errorf("signal %d out of range", n)
		}
		return syscall.Signal(n), s, nil
	}
	name := strings.TrimPrefix(strings.ToUpper(s), "SIG")
	if sig, ok := signalNames[name]; ok {
		return sig, "SIG" + name, nil
	}
	return 0, "", fmt.Errorf("unknown signal %q", s)
}

// killOpts are the --kill modifiers beyond the targets themselves.
type killOpts struct {
	sig     syscall.Signal // 0 = SIGTERM with SIGKILL escalation (or --force)
	sigName string
	wait    time.Duration // 0 = do not wait for the ports to free up
}

// parseKillOpts (pure) reads --signal and --wait. --signal needs a value and
// excludes --force; --wait takes optional whole seconds.
func parseKillOpts(args []string, force bool) (killOpts, error) {
	var o killOpts
	if hasFlag(args, "--signal") {
		v := flagArgs(args, "--signal")
		if len(v) == 0 {
			return o, errors.New("--signal needs a signal name or number")
		}
		if force {
			return o, errors.New("--signal and --force cannot be combined")
		}
		sig, name, err := parseSignal(v[0])
		if err != nil {
			return o, err
		}
		o.sig, o.sigName = sig, name
	}
	if hasFlag(args, "--wait") {
		o.wait = defaultWait
		if v := flagArgs(args, "--wait"); len(v) > 0 {
			n, err := strconv.Atoi(v[0])
			if err != nil || n < 1 {
				return o, fmt.Errorf("--wait needs whole seconds, got %q", v[0])
			}
			o.wait = time.Duration(n) * time.Second
		}
	}
	return o, nil
}

// stopFunc returns how cliKill stops each listener: the default stopItem, or
// a single chosen signal when --signal was given.
func (o killOpts) stopFunc() func(PortItem, bool) error {
	if o.sig == 0 {
		return stopItem
	}
	return func(it PortItem, _ bool) error { return signalItem(it, o.sig, o.sigName) }
}

// verb names what a stop sends, for the dry-run preview.
func (o killOpts) verb(force bool) string {
	switch {
	case o.sig != 0:
		return o.sigName
	case force:
		return "SIGKILL"
	}
	return "SIGTERM"
}

// signalItem sends one chosen signal with no SIGKILL escalation. Containers
// get it through their runtime and systemd services through systemctl, so the
// supervisor sees the signal as deliberate rather than as a crash.
func signalItem(it PortItem, sig syscall.Signal, name string) error {
	if it.IsContainer() {
		return containerCmd(runtimeOf(it.Source), it.ctrUser, "kill", "--signal", name, it.PID)
	}
	pid, err := hostPID(it)
	if err != nil {
		return err
	}
	if unit, ok := serviceUnit(pid); ok {
		return systemctl("kill", "--signal="+name, unit)
	}
	return sendSignal(pid, sig)
}

// selectTargets (pure) returns the listeners on a requested port or whose
// process name (container name without its runtime prefix) was requested.
func selectTargets(ports, names []string, items []PortItem) []PortItem {
	p, n := targetSet(ports), targetSet(names)
	var out []PortItem
	for _, it := range items {
		if p[it.Port] || n[containerName(it)] {
			out = append(out, it)
		}
	}
	return out
}

// portsOf (pure) lists the distinct ports of items in order.
func portsOf(items []PortItem) []string {
	seen := map[string]bool{}
	var ports []string
	for _, it := range items {
		if !seen[it.Port] {
			seen[it.Port] = true
			ports = append(ports, it.Port)
		}
	}
	return ports
}

// waitFree rescans until none of ports has a listener or timeout passes, and
// returns the ports that are still taken.
func waitFree(ports []string, timeout time.Duration) []string {
	want := targetSet(ports)
	busy := func() []string {
		items, err := activePorts()
		if err != nil {
			// ss failed: nothing is known, so every port counts as held.
			return append([]string(nil), ports...)
		}
		var held []PortItem
		for _, it := range items {
			if want[it.Port] {
				held = append(held, it)
			}
		}
		return portsOf(held)
	}
	for i := 0; i < int(timeout/waitPoll); i++ {
		if b := busy(); len(b) == 0 {
			return nil
		}
		sleepFn(waitPoll)
	}
	return busy()
}

// waitAndReport runs --wait after a successful stop. It returns false when a
// port is still taken once the timeout passes.
func waitAndReport(ports []string, timeout time.Duration) bool {
	held := waitFree(ports, timeout)
	for _, p := range held {
		fmt.Fprintf(os.Stderr, "port %s still in use after %s\n", p, timeout)
	}
	return len(held) == 0
}
