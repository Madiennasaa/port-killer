package main

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

type Event struct {
	Time    time.Time `json:"t"`
	Kind    string    `json:"kind"` // muncul | hilang
	Proto   string    `json:"proto"`
	Port    string    `json:"port"`
	Process string    `json:"proc"`
	PID     string    `json:"pid"`
	Cause   string    `json:"cause,omitempty"`
}

func evKey(it PortItem) string { return it.Proto + "|" + it.Port + "|" + it.PID }

// diffPorts murni: event muncul/hilang antara dua snapshot.
func diffPorts(prev, cur []PortItem, now time.Time, cause string) []Event {
	p := map[string]PortItem{}
	for _, it := range prev {
		p[evKey(it)] = it
	}
	c := map[string]bool{}
	var out []Event
	for _, it := range cur {
		k := evKey(it)
		c[k] = true
		if _, ok := p[k]; !ok {
			out = append(out, Event{Time: now, Kind: "muncul", Proto: it.Proto, Port: it.Port, Process: it.Process, PID: it.PID})
		}
	}
	for _, it := range prev {
		if !c[evKey(it)] {
			out = append(out, Event{Time: now, Kind: "hilang", Proto: it.Proto, Port: it.Port, Process: it.Process, PID: it.PID, Cause: cause})
		}
	}
	return out
}

// formatEvent murni.
func formatEvent(e Event) string {
	s := e.Time.Format("15:04:05") + "  " + e.Kind + "  " + e.Proto + ":" + e.Port + "  " + e.Process
	if e.Cause != "" {
		s += "  (" + e.Cause + ")"
	}
	return s
}

// trimLines murni: simpan max baris terakhir.
func trimLines(s string, max int) string {
	l := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(l) > max {
		l = l[len(l)-max:]
	}
	return strings.Join(l, "\n") + "\n"
}

func eventsPath() string {
	home, _ := os.UserHomeDir()
	if su := os.Getenv("SUDO_USER"); su != "" {
		if u, err := user.Lookup(su); err == nil && u.HomeDir != "" {
			home = u.HomeDir
		}
	}
	return filepath.Join(home, ".local", "state", "portkiller", "events.jsonl")
}

const maxEvents = 1000

func appendEvents(path string, evs []Event) {
	if len(evs) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	for _, e := range evs {
		if b, err := json.Marshal(e); err == nil {
			f.Write(append(b, '\n'))
		}
	}
	f.Close()
	chownToSudoUser(filepath.Dir(path), path)
	if b, err := os.ReadFile(path); err == nil && strings.Count(string(b), "\n") > maxEvents+200 {
		os.WriteFile(path, []byte(trimLines(string(b), maxEvents)), 0o644)
	}
}

// readEvents: n terakhir, terbaru dulu.
func readEvents(path string, n int) []Event {
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
	var out []Event
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, all[i])
	}
	return out
}
