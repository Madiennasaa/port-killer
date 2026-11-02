package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// watchInterval is how often the watch mode re-scans the ports.
var watchInterval = 2 * time.Second

// newRealWatchCtx builds the production signal context.
func newRealWatchCtx() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx
}

// newWatchCtx is a seam so tests can replace the signal context.
var newWatchCtx = newRealWatchCtx

// watchWarn is a seam for runWatch's stderr warnings.
var watchWarn = func(s string) { fmt.Fprintln(os.Stderr, "warning:", s) }

// watchPort validates --port for the watch mode: absent means "all ports".
func watchPort(args []string) (string, error) {
	if !hasFlag(args, "--port") {
		return "", nil
	}
	n, err := intFlag(args, "--port", 0)
	if err != nil {
		return "", err
	}
	if n < 1 || n > 65535 {
		return "", fmt.Errorf("--port must be 1..65535")
	}
	return strconv.Itoa(n), nil
}

// watchEvents (pure): the appeared/vanished diff between two snapshots plus
// fresh guard violations. An optional port filter narrows both.
func watchEvents(port string, prev, cur []PortItem, now time.Time, rules []Rule) []Event {
	out := make([]Event, 0, len(prev)+len(cur))
	out = append(out, diffPorts(prev, cur, now, "watch")...)
	for _, v := range checkGuard(rules, cur) {
		out = append(out, Event{
			Time: now, Kind: "guard", Proto: v.Item.Proto, Port: v.Item.Port,
			Process: v.Item.Process, PID: v.Item.PID,
			Cause: "owned by " + v.Rule.Owner,
		})
	}
	if port == "" {
		return out
	}
	var filtered []Event
	for _, e := range out {
		if e.Port == port {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// runWatch scans on every interval and prints port appear/vanish/guard events.
// The seed pass is silent: nothing existed before it, so every listener would
// otherwise be reported as "appeared" on startup. A scan failure becomes a
// stderr warning and the previous snapshot is kept.
func runWatch(
	ctx context.Context,
	port string,
	notify bool,
	scan func() ([]PortItem, error),
	print func(string),
	interval time.Duration,
	rules []Rule,
	notifyFn func(title, body string) error,
	warn func(string),
) {
	var prev []PortItem
	seeded := false
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		items, err := scan()
		if err != nil {
			warn(err.Error())
		}
		for _, e := range watchEvents(port, prev, items, time.Now(), rules) {
			// The seed pass stays silent for appear/vanish: nothing existed
			// "before" it, so every listener would show up as appeared. Guard
			// violations still surface: they are true right now.
			if !seeded && e.Kind != "guard" {
				continue
			}
			print(formatEvent(e))
			if notify {
				_ = notifyFn("portkiller", formatEvent(e))
			}
		}
		seeded = true
		// Keep the good snapshot on a failed scan so a partial result cannot
		// flood "vanished" events.
		if err == nil {
			prev = items
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// cliWatch implements `--watch [--port N] [--notify]` until interrupted.
func cliWatch(args []string) {
	port, err := watchPort(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		exitProcess(2)
		return
	}
	fmt.Fprintln(os.Stderr, "watching ports (Ctrl+C to stop)")
	runWatch(newWatchCtx(), port, hasFlag(args, "--notify"), activePorts,
		func(l string) { fmt.Println(l) }, watchInterval, loadGuard(), notifySend, watchWarn)
}
