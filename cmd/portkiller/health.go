package main

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type HealthStatus int

const (
	HealthUnknown HealthStatus = iota
	HealthAlive
	HealthDead
	HealthNotApplicable
)

type healthResult struct {
	status  HealthStatus
	latency time.Duration
}

type healthResultMsg struct {
	results  map[string]healthResult
	panicked bool
}

// healthKey ties health results to items from a refresh.
func healthKey(proto, addr, port, pid string) string {
	return proto + "-" + addr + "-" + port + "-" + pid
}

// dialTarget (pure): wildcard -> 127.0.0.1:port.
func dialTarget(addr, port string) string {
	a := strings.TrimSpace(addr)
	if a == "" || a == "0.0.0.0" || a == "*" || a == "::" || a == "[::]" {
		return "127.0.0.1:" + port
	}
	if strings.Contains(a, ":") && !strings.HasPrefix(a, "[") {
		a = "[" + a + "]"
	}
	return a + ":" + port
}

func checkOne(addr, port string, timeout time.Duration) (HealthStatus, time.Duration) {
	start := time.Now()
	c, err := net.DialTimeout("tcp", dialTarget(addr, port), timeout)
	if err != nil {
		return HealthDead, 0
	}
	d := time.Since(start)
	c.Close()
	return HealthAlive, d
}

// CheckHealth runs up to 16 dials in parallel, UDP -> NotApplicable. Called via tea.Cmd so the UI never blocks.
func CheckHealth(items []PortItem) map[string]healthResult {
	out := make(map[string]healthResult)
	type job struct {
		key  string
		addr string
		port string
	}
	var jobs []job
	for _, it := range items {
		k := healthKey(it.Proto, it.Addr, it.Port, it.PID)
		if strings.HasPrefix(it.Proto, "udp") {
			out[k] = healthResult{status: HealthNotApplicable}
			continue
		}
		if _, err := strconv.Atoi(it.Port); err != nil {
			out[k] = healthResult{status: HealthDead}
			continue
		}
		jobs = append(jobs, job{k, it.Addr, it.Port})
	}
	sem := make(chan struct{}, 16)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			st, lat := checkOne(j.addr, j.port, 300*time.Millisecond)
			mu.Lock()
			out[j.key] = healthResult{status: st, latency: lat}
			mu.Unlock()
		}(j)
	}
	wg.Wait()
	return out
}
