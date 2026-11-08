package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"time"
)

// ---------- Messages ----------
type tickMsg time.Time

const messageTTL = 10 * time.Second

type portsMsg struct {
	Items []PortItem
	Err   error
}

const msgHealthPanic = "Health check panicked, skipped this cycle."

type actionMsg struct {
	item  PortItem
	force bool
	err   error
}

type blastMsg struct {
	pid   string
	port  string
	blast Blast
}

type projectMsg struct {
	project string
	done    int
	err     error
	stopped []string
}

type orphansMsg struct {
	done    int
	err     error
	stopped []string
}

type batchMsg struct {
	done    int
	err     error
	stopped []string
}

// stopEach stops every item and returns how many succeeded plus the first
// error, so one failure does not abort the rest of a batch.
func stopEach(items []PortItem, force bool) (done int, firstErr error) {
	stopped, firstErr := stopEachKeys(items, force)
	return len(stopped), firstErr
}

// stopEachKeys is stopEach that also names the PIDs (container ids) stopped.
func stopEachKeys(items []PortItem, force bool) (stopped []string, firstErr error) {
	for _, it := range items {
		if err := stopItem(it, force); err == nil {
			stopped = append(stopped, it.PID)
		} else if firstErr == nil {
			firstErr = err
		}
	}
	return stopped, firstErr
}

// stopBatchCmd stops the rows marked with Space.
func stopBatchCmd(targets []PortItem, force bool) tea.Cmd {
	snap := make([]PortItem, len(targets))
	copy(snap, targets)
	return func() tea.Msg {
		stopped, err := stopEachKeys(snap, force)
		return batchMsg{done: len(stopped), err: err, stopped: stopped}
	}
}

func blastCmd(it PortItem) tea.Cmd {
	return func() tea.Msg {
		return blastMsg{pid: it.PID, port: it.Port, blast: ComputeBlast(it)}
	}
}

func killProjectCmd(project string, targets []PortItem) tea.Cmd {
	snap := make([]PortItem, len(targets))
	copy(snap, targets)
	return func() tea.Msg {
		stopped, err := stopEachKeys(snap, false)
		return projectMsg{project: project, done: len(stopped), err: err, stopped: stopped}
	}
}

func refreshCmd() tea.Msg {
	items, err := scanPorts()
	return portsMsg{Items: items, Err: err}
}

// checkHealth is a seam so healthCmd's recover path is testable.
var checkHealth = CheckHealth

func healthCmd(items []PortItem) tea.Cmd {
	snap := make([]PortItem, len(items))
	copy(snap, items)
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = healthResultMsg{results: map[string]healthResult{}, panicked: true}
			}
		}()
		msg = healthResultMsg{results: checkHealth(snap)}
		return msg
	}
}

// tickMsgAt wraps a tick timestamp in a msg; a named function (not a closure)
// so the callback is testable without waiting out the real 2s tick.
func tickMsgAt(t time.Time) tea.Msg { return tickMsg(t) }

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, tickMsgAt)
}

func killCmd(item PortItem, force bool) tea.Cmd {
	return func() tea.Msg {
		return actionMsg{item: item, force: force, err: stopItem(item, force)}
	}
}

// killOrphansCmd: SIGTERM semua orphan host, lewati Docker.
func killOrphansCmd(items []PortItem) tea.Cmd {
	var snap []PortItem
	for _, it := range items {
		if !it.IsContainer() {
			snap = append(snap, it)
		}
	}
	return func() tea.Msg {
		stopped, err := stopEachKeys(snap, false)
		return orphansMsg{done: len(stopped), err: err, stopped: stopped}
	}
}
