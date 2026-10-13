package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"time"
)

// ---------- Messages ----------
type tickMsg time.Time

const messageTTL = 10 * time.Second

type portsMsg []PortItem

const msgHealthPanic = "Health check panic, dilewati siklus ini."

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
}

type orphansMsg struct {
	done int
	err  error
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
		done := 0
		var firstErr error
		for _, it := range snap {
			var err error
			if it.IsDocker() {
				err = StopContainer(it.PID)
			} else {
				err = KillProcess(it.PID, false)
			}
			if err == nil {
				done++
			} else if firstErr == nil {
				firstErr = err
			}
		}
		return projectMsg{project: project, done: done, err: firstErr}
	}
}

func refreshCmd() tea.Msg { return portsMsg(GetActivePorts()) }

func healthCmd(items []PortItem) tea.Cmd {
	snap := make([]PortItem, len(items))
	copy(snap, items)
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = healthResultMsg{results: map[string]healthResult{}, panicked: true}
			}
		}()
		msg = healthResultMsg{results: CheckHealth(snap)}
		return msg
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func killCmd(item PortItem, force bool) tea.Cmd {
	return func() tea.Msg {
		var err error
		if item.IsDocker() {
			err = StopContainer(item.PID)
		} else {
			err = KillProcess(item.PID, force)
		}
		return actionMsg{item: item, force: force, err: err}
	}
}

// killOrphansCmd: SIGTERM semua orphan host, lewati Docker.
func killOrphansCmd(items []PortItem) tea.Cmd {
	snap := make([]PortItem, len(items))
	copy(snap, items)
	return func() tea.Msg {
		done := 0
		var firstErr error
		for _, it := range snap {
			if it.IsDocker() {
				continue
			}
			if err := KillProcess(it.PID, false); err == nil {
				done++
			} else if firstErr == nil {
				firstErr = err
			}
		}
		return orphansMsg{done: done, err: firstErr}
	}
}
