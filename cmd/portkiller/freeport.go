package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// portFree reports whether a TCP listener can bind port. It is a seam so the
// planner is testable without touching real sockets.
var portFree = func(port int) bool {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// freeArgs (pure): find the range argument that follows --free.
func freeArgs(args []string) (string, bool) {
	for i, a := range args {
		if a != "--free" || i+1 >= len(args) {
			continue
		}
		spec := args[i+1]
		if spec == "" || strings.HasPrefix(spec, "--") {
			return "", false
		}
		return spec, true
	}
	return "", false
}

// parsePort (pure) accepts one port from 1..65535.
func parsePort(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("bad port %q", s)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("port %d out of range (1..65535)", n)
	}
	return n, nil
}

// parsePortRange (pure) accepts "N" or "START-END" inside 1..65535.
func parsePortRange(spec string) (start, end int, err error) {
	lo, hi, hasRange := strings.Cut(spec, "-")
	if lo == "" || (hasRange && hi == "") {
		return 0, 0, errors.New("empty port range")
	}
	start, err = parsePort(lo)
	if err != nil {
		return 0, 0, err
	}
	end = start
	if !hasRange {
		return start, end, nil
	}
	end, err = parsePort(hi)
	if err != nil {
		return 0, 0, err
	}
	if start > end {
		return 0, 0, fmt.Errorf("range start %d is after end %d", start, end)
	}
	return start, end, nil
}

// firstFreePort returns the first bindable port in [start, end].
func firstFreePort(start, end int) (int, bool) {
	for p := start; p <= end; p++ {
		if portFree(p) {
			return p, true
		}
	}
	return 0, false
}

// cliFree prints the first free port in the requested range. Exit codes:
// 1 = nothing free, 2 = bad --free argument.
func cliFree(args []string, asJSON bool) {
	spec, ok := freeArgs(args)
	if !ok {
		fmt.Fprintln(os.Stderr, "--free needs a port range (e.g. --free 3000-3010)")
		exitProcess(2)
		return
	}
	start, end, err := parsePortRange(spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --free range %q: %v\n", spec, err)
		exitProcess(2)
		return
	}
	port, ok := firstFreePort(start, end)
	if !ok {
		fmt.Fprintf(os.Stderr, "no free port in %s\n", spec)
		exitProcess(1)
		return
	}
	if asJSON {
		fmt.Printf("{\"port\":%d}\n", port)
		return
	}
	fmt.Println(port)
}
