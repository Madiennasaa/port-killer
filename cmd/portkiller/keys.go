package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// footerKeys: same order as the footer; width = len(key)+len(label).
var footerKeys = []struct{ key, label, send string }{
	{"F3", "Search ", "/"}, {"F4", "Proto ", "t"}, {"F5", "Refresh ", "r"}, {"F6", "Sort ", "s"},
	{"F8", "Force ", "K"}, {"F9", "Kill ", "enter"}, {"o", "Orphan ", "o"}, {"X", "Clean ", "X"},
	{"i", "Detail ", "i"}, {"Spc", "Mark ", " "}, {"b", "Browser ", "b"}, {"H", "Hide ", "H"}, {"h", "History ", "h"}, {"P", "StopProject ", "P"}, {"?", "Help ", "?"},
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
	case modeConfirmProject, modeConfirmOrphans, modeConfirm, modeConfirmBatch:
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
	case modeConfirmBatch:
		m.message, m.isErr = fmt.Sprintf("Stopping %d selected...", len(m.btarget)), false
		m.restarts = captureAll(m.btarget)
		m.marked = nil
		return m, stopBatchCmd(m.btarget, m.force)
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

// moveKey applies a cursor-movement key. handled is false for anything else.
func (m model) moveKey(key string) (model, bool) {
	switch key {
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
	default:
		return m, false
	}
	return m, true
}

// startKill arms the stop dialog (force picks SIGKILL). ok is false when the
// key is not a kill key; cmd carries the blast-info request when a dialog
// actually opened.
func (m model) startKill(key string) (model, tea.Cmd, bool) {
	var force bool
	switch key {
	case "enter", "f9":
		force = false
	case "K", "f8":
		force = true
	default:
		return m, nil, false
	}
	if m.busy {
		return m, nil, true
	}
	m.askKill(force)
	if m.mode != modeConfirm {
		return m, nil, true
	}
	return m, blastCmd(m.target), true
}

// splitSupervised (pure) separates what R may relaunch from what a process
// manager owns; skipped describes the latter as "name (pm2)".
func splitSupervised(rs []Restartable) (run []Restartable, skipped []string) {
	for _, r := range rs {
		if r.Supervisor != "" {
			skipped = append(skipped, r.Name+" ("+r.Supervisor+")")
			continue
		}
		run = append(run, r)
	}
	return run, skipped
}

// restartKey triggers "restart last stopped". ok is false for other keys.
func (m model) restartKey(key string) (model, tea.Cmd, bool) {
	if key != "R" {
		return m, nil, false
	}
	if m.busy {
		return m, nil, true
	}
	if len(m.restarts) == 0 {
		m.message, m.isErr = "No recently stopped process to restart.", false
		return m, nil, true
	}
	run, skipped := splitSupervised(m.restarts)
	if len(run) == 0 {
		m.message, m.isErr = "Not restarted: "+strings.Join(skipped, ", ")+" (restart it in its process manager)", true
		return m, nil, true
	}
	m.busy = true
	m.message, m.isErr = "Restarting: "+restartLabel(run[0])+" — "+restartDesc(run[0])+"...", false
	if len(skipped) > 0 {
		m.message += " (skipped " + strings.Join(skipped, ", ") + ")"
	}
	return m, restartCmd(run), true
}

// normalKey handles the main keymap once no overlay or dialog is up.
func (m model) normalKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+c", "q", "f10":
		return m, tea.Quit
	case "r", "f5":
		return m, refreshCmd
	}
	if nm, handled := m.moveKey(key); handled {
		m = nm
	} else if nm, cmd, handled := m.startKill(key); handled {
		if cmd != nil {
			return nm, cmd
		}
		m = nm
	} else if nm, cmd, handled := m.restartKey(key); handled {
		if cmd != nil {
			return nm, cmd
		}
		m = nm
	} else {
		m = m.viewToggleKey(key)
	}
	m.clamp()
	m.refreshDetail()
	return m, nil
}

// viewToggleKey applies the display/scope toggles and overlays.
func (m model) viewToggleKey(key string) model {
	switch key {
	case "/", "f3":
		m.mode = modeFilter
	case "esc":
		m.filter = ""
		m.marked = nil
		m.rebuild()
	case " ":
		m = m.toggleMark()
	case "b":
		m = m.openSelected()
	case "H":
		m = m.toggleHidden()
	case "U":
		m.showHidden = !m.showHidden
		m.rebuild()
	case "t", "f4":
		m.proto = (m.proto + 1) % len(protoNames)
		m.rebuild()
	case "s", "f6":
		m.sortBy = (m.sortBy + 1) % len(sortNames)
		m.rebuild()
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
	return m
}

// toggleMark flips the Space mark on the cursor row and moves down, so a run
// of rows can be marked by holding Space.
func (m model) toggleMark() model {
	if len(m.view) == 0 {
		return m
	}
	k := itemKey(m.view[m.cursor])
	if m.marked == nil {
		m.marked = map[string]bool{}
	}
	if m.marked[k] {
		delete(m.marked, k)
	} else {
		m.marked[k] = true
	}
	m.cursor++
	return m
}

// openSelected opens the cursor row's URL in the desktop user's browser.
func (m model) openSelected() model {
	if len(m.view) == 0 {
		return m
	}
	url, err := openInBrowser(m.view[m.cursor])
	if err != nil {
		m.message, m.isErr = "Browser: "+err.Error(), true
		return m
	}
	m.message, m.isErr = "Opened "+url, false
	return m
}

// toggleHidden hides the cursor row's process name (or unhides it when it is
// already hidden and shown via U) and persists the set.
func (m model) toggleHidden() model {
	if len(m.view) == 0 {
		return m
	}
	name := m.view[m.cursor].Process
	hidden := make(map[string]bool, len(m.hidden)+1)
	for n := range m.hidden {
		hidden[n] = true
	}
	verb := "Hidden"
	if hidden[name] {
		delete(hidden, name)
		verb = "Unhidden"
	} else {
		hidden[name] = true
	}
	if err := saveHidden(hiddenPath(), hidden); err != nil {
		m.message, m.isErr = "Hide: "+err.Error(), true
		return m
	}
	m.hidden = hidden
	m.message, m.isErr = verb+" "+name+" (U shows hidden rows)", false
	m.rebuild()
	return m
}
