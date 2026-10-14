package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"os"
)

// footerKeys: same order as the footer; width = len(key)+len(label).
var footerKeys = []struct{ key, label, send string }{
	{"F3", "Search ", "/"}, {"F4", "Proto ", "t"}, {"F5", "Refresh ", "r"}, {"F6", "Sort ", "s"},
	{"F8", "Force ", "K"}, {"F9", "Kill ", "enter"}, {"o", "Orphan ", "o"}, {"X", "Clean ", "X"},
	{"i", "Detail ", "i"}, {"h", "History ", "h"}, {"P", "StopProject ", "P"}, {"?", "Help ", "?"},
	{"R", "Restart ", "R"}, {"c", "Conflicts ", "c"}, {"F10", "Quit ", "q"},
}

func keyFromString(s string) tea.KeyMsg {
	if s == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.showHelp || m.showHistory || m.mode != modeNormal {
		return m, nil
	}
	switch {
	case msg.Button == tea.MouseButtonWheelUp && msg.Action == tea.MouseActionPress:
		m.cursor--
		m.clamp()
		m.refreshDetail()
		return m, nil
	case msg.Button == tea.MouseButtonWheelDown && msg.Action == tea.MouseActionPress:
		m.cursor++
		m.clamp()
		m.refreshDetail()
		return m, nil
	}
	if msg.Action != tea.MouseActionPress || (msg.Button != tea.MouseButtonLeft && msg.Button != tea.MouseButtonRight) {
		return m, nil
	}
	if msg.Y == m.height-1 {
		x := 0
		for _, f := range footerKeys {
			w := len(f.key) + len(f.label)
			if msg.X >= x && msg.X < x+w {
				return m.handleKey(keyFromString(f.send))
			}
			x += w
		}
		return m, nil
	}
	row := msg.Y - 1
	if row >= 0 && row < m.rows() {
		idx := m.offset + row
		if idx < len(m.view) {
			again := idx == m.cursor
			m.cursor = idx
			m.clamp()
			m.refreshDetail()
			if again || msg.Button == tea.MouseButtonRight {
				return m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
			}
		}
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.showHistory {
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		m.showHistory = false
		return m, nil
	}

	if m.showHelp {
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		m.showHelp = false
		m.helpOverride = nil
		return m, nil
	}

	switch m.mode {
	case modeConfirmProject:
		m.mode = modeNormal
		if key == "y" || key == "Y" {
			m.busy = true
			m.message, m.isErr = "Stopping project "+m.pname+"...", false
			m.markCause()
			m.restarts = captureAll(m.ptarget)
			return m, killProjectCmd(m.pname, m.ptarget)
		}
		m.message, m.isErr = "Cancelled.", false
		return m, nil

	case modeConfirmOrphans:
		m.mode = modeNormal
		if key == "y" || key == "Y" {
			m.busy = true
			m.message, m.isErr = "Cleaning orphans (SIGTERM)...", false
			m.markCause()
			m.restarts = captureAll(m.otarget)
			return m, killOrphansCmd(m.otarget)
		}
		m.message, m.isErr = "Cancelled.", false
		return m, nil

	case modeConfirm:
		m.mode = modeNormal
		if key == "y" || key == "Y" {
			m.busy = true
			m.message, m.isErr = "Processing...", false
			m.markCause()
			m.restarts = captureAll([]PortItem{m.target})
			return m, killCmd(m.target, m.force)
		}
		m.message, m.isErr = "Cancelled.", false
		return m, nil

	case modeFilter:
		switch key {
		case "enter":
			m.mode = modeNormal
		case "esc":
			m.mode = modeNormal
			m.filter = ""
		case "backspace":
			r := []rune(m.filter)
			if len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.filter += string(msg.Runes)
			}
		}
		m.rebuild()
		return m, nil
	}

	switch key {
	case "ctrl+c", "q", "f10":
		return m, tea.Quit
	case "up", "k":
		m.cursor--
	case "down", "j":
		m.cursor++
	case "pgup":
		m.cursor -= m.rows()
	case "pgdown":
		m.cursor += m.rows()
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(m.view) - 1
	case "/", "f3":
		m.mode = modeFilter
	case "esc":
		m.filter = ""
		m.rebuild()
	case "t", "f4":
		m.proto = (m.proto + 1) % len(protoNames)
		m.rebuild()
	case "r", "f5":
		return m, refreshCmd
	case "s", "f6":
		m.sortBy = (m.sortBy + 1) % len(sortNames)
		m.rebuild()
	case "enter", "f9":
		if !m.busy {
			m.askKill(false)
			if m.mode == modeConfirm {
				return m, blastCmd(m.target)
			}
		}
	case "K", "f8":
		if !m.busy {
			m.askKill(true)
			if m.mode == modeConfirm {
				return m, blastCmd(m.target)
			}
		}
	case "i", "tab":
		m.showDetail = !m.showDetail
	case "h":
		m.events = readEvents(eventsPath(), 200)
		m.showHistory = true
	case "c":
		wd, _ := os.Getwd()
		m.helpOverride = append([]string{" Conflicts for " + wd, ""}, conflictLines(readNeeds(wd), m.all)...)
		m.showHelp = true
	case "?", "f1":
		m.showHelp = true
	case "R":
		if m.busy {
			break
		}
		if len(m.restarts) == 0 {
			m.message, m.isErr = "No recently stopped process to restart.", false
		} else {
			m.busy = true
			m.message, m.isErr = "Restarting: "+restartDesc(m.restarts[0])+"...", false
			return m, restartCmd(m.restarts)
		}
	case "o":
		m.orphans = !m.orphans
		m.rebuild()
	case "P":
		if !m.busy {
			m.askStopProject()
		}
	case "X":
		if !m.busy {
			m.askCleanOrphans()
		}
	}
	m.clamp()
	m.refreshDetail()
	return m, nil
}
