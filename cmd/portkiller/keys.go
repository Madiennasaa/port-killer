package main

import (
	tea "github.com/charmbracelet/bubbletea"
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

// mouseWheel applies a wheel delta to the cursor.
func (m model) mouseWheel(delta int) model {
	m.cursor += delta
	m.clamp()
	m.refreshDetail()
	return m
}

// footerClick maps an x position on the footer line to a footer key, and
// dispatches that key. ok is false when the click hit a gap between keys.
func (m model) footerClick(x int) (model, tea.Cmd, bool) {
	at := 0
	for _, f := range footerKeys {
		w := len(f.key) + len(f.label)
		if x >= at && x < at+w {
			nm, cmd := m.handleKey(keyFromString(f.send))
			return nm.(model), cmd, true
		}
		at += w
	}
	return m, nil, false
}

// rowClick selects the row under y. Clicking the already-selected row (or
// right-clicking any row) opens the kill dialog for it.
func (m model) rowClick(y int, right bool) (model, tea.Cmd) {
	row := y - 1
	if row < 0 || row >= m.rows() {
		return m, nil
	}
	idx := m.offset + row
	if idx >= len(m.view) {
		return m, nil
	}
	again := idx == m.cursor
	m.cursor = idx
	m.clamp()
	m.refreshDetail()
	if again || right {
		nm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
		return nm.(model), cmd
	}
	return m, nil
}

func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.showHelp || m.showHistory || m.mode != modeNormal {
		return m, nil
	}
	if msg.Action == tea.MouseActionPress {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			return m.mouseWheel(-1), nil
		case tea.MouseButtonWheelDown:
			return m.mouseWheel(1), nil
		}
	}
	if msg.Action != tea.MouseActionPress ||
		(msg.Button != tea.MouseButtonLeft && msg.Button != tea.MouseButtonRight) {
		return m, nil
	}
	if msg.Y == m.height-1 {
		if nm, cmd, ok := m.footerClick(msg.X); ok {
			return nm, cmd
		}
		return m, nil
	}
	return m.rowClick(msg.Y, msg.Button == tea.MouseButtonRight)
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.showHistory || m.showHelp {
		return m.closeOverlay(key)
	}

	switch m.mode {
	case modeConfirmProject, modeConfirmOrphans, modeConfirm:
		return m.confirmKey(key)
	case modeFilter:
		return m.filterKey(msg, key)
	}
	return m.normalKey(key)
}

// closeOverlay dismisses the history/help overlay on any key but ctrl+c.
func (m model) closeOverlay(key string) (tea.Model, tea.Cmd) {
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	m.showHistory = false
	m.showHelp = false
	m.helpOverride = nil
	return m, nil
}

// confirmKey answers one of the pending confirmation dialogs.
func (m model) confirmKey(key string) (tea.Model, tea.Cmd) {
	mode := m.mode
	m.mode = modeNormal
	if key != "y" && key != "Y" {
		m.message, m.isErr = "Cancelled.", false
		return m, nil
	}
	m.busy = true
	m.markCause()
	switch mode {
	case modeConfirmProject:
		m.message, m.isErr = "Stopping project "+m.pname+"...", false
		m.restarts = captureAll(m.ptarget)
		return m, killProjectCmd(m.pname, m.ptarget)
	case modeConfirmOrphans:
		m.message, m.isErr = "Cleaning orphans (SIGTERM)...", false
		m.restarts = captureAll(m.otarget)
		return m, killOrphansCmd(m.otarget)
	default:
		m.message, m.isErr = "Processing...", false
		m.restarts = captureAll([]PortItem{m.target})
		return m, killCmd(m.target, m.force)
	}
}

// filterKey edits the search filter while modeFilter is active.
func (m model) filterKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter", "esc":
		m.mode = modeNormal
		if key == "esc" {
			m.filter = ""
		}
	case "backspace":
		if r := []rune(m.filter); len(r) > 0 {
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

// normalKey handles the main keymap once no overlay or dialog is up.
func (m model) normalKey(key string) (tea.Model, tea.Cmd) {
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
		wd := conflictDir()
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
