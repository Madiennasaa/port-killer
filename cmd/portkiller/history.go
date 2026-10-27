package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// loadEvents reads every valid event in append order. Malformed lines (a torn
// write, a partial line) are skipped instead of failing the whole history.
func loadEvents(path string) []Event {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var all []Event
	for _, l := range strings.Split(string(b), "\n") {
		var e Event
		if json.Unmarshal([]byte(l), &e) == nil && e.Kind != "" {
			all = append(all, e)
		}
	}
	return all
}

// historyEvents (pure) returns the newest events first, optionally filtered to
// one port, capped at limit.
func historyEvents(all []Event, port string, limit int) []Event {
	out := make([]Event, 0, limit)
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		if port != "" && all[i].Port != port {
			continue
		}
		out = append(out, all[i])
	}
	return out
}

// formatHistory is the CLI rendering: full date, since a history spans days.
func formatHistory(e Event) string {
	s := e.Time.Format("2006-01-02 15:04:05") + "  " + e.Kind + "  " + e.Proto + ":" + e.Port + "  " + e.Process
	if e.Cause != "" {
		s += "  (" + e.Cause + ")"
	}
	return s
}

// flagValue returns the value that follows name. ok is false when the flag is
// absent or when its value is missing or looks like another flag.
func flagValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a != name || i+1 >= len(args) {
			continue
		}
		v := args[i+1]
		if v == "" || strings.HasPrefix(v, "--") {
			return "", false
		}
		return v, true
	}
	return "", false
}

// intFlag parses an integer flag, returning def when it is absent.
func intFlag(args []string, name string, def int) (int, error) {
	if !hasFlag(args, name) {
		return def, nil
	}
	v, ok := flagValue(args, name)
	if !ok {
		return 0, fmt.Errorf("%s needs a number", name)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s needs a number, got %q", name, v)
	}
	return n, nil
}

// cliHistory prints recent appear/vanished events, newest first. Exit codes:
// 2 = bad flag value; an empty history is not an error.
func cliHistory(args []string, asJSON bool) {
	limit, err := intFlag(args, "--limit", 20)
	if err != nil || limit < 1 || limit > maxEvents {
		fmt.Fprintf(os.Stderr, "invalid --limit: %v (1..%d)\n", err, maxEvents)
		exitProcess(2)
		return
	}
	port := ""
	if hasFlag(args, "--port") {
		n, err := intFlag(args, "--port", 0)
		if err != nil || n < 1 || n > 65535 {
			fmt.Fprintf(os.Stderr, "invalid --port: %v (1..65535)\n", err)
			exitProcess(2)
			return
		}
		port = strconv.Itoa(n)
	}

	evs := historyEvents(loadEvents(eventsPath()), port, limit)
	if asJSON {
		b, _ := json.MarshalIndent(evs, "", "  ")
		fmt.Println(string(b))
		return
	}
	for _, e := range evs {
		fmt.Println(formatHistory(e))
	}
}
