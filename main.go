package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type model struct {
	items   []PortItem
	cursor  int
	message string
	confirm bool
}

func initialModel() model {
	return model{items: GetActivePorts()}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m *model) clampCursor() {
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	// Mode konfirmasi
	if m.confirm {
		switch keyMsg.String() {
		case "y", "Y":
			item := m.items[m.cursor]
			var err error

			if strings.HasPrefix(item.Process, "docker:") {
				err = StopContainer(item.PID)
			} else {
				err = KillProcess(item.PID, false)
			}

			if err != nil {
				m.message = fmt.Sprintf("Gagal: %v", err)
			} else {
				m.message = fmt.Sprintf("Berhasil menghentikan %s (port %s)", item.Process, item.Port)
				m.items = GetActivePorts()
				m.clampCursor()
			}
		default:
			m.message = "Dibatalkan."
		}
		m.confirm = false
		return m, nil
	}

	switch keyMsg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}

	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}

	case "r":
		m.items = GetActivePorts()
		m.clampCursor()
		m.message = "Daftar port diperbarui."

	case "enter":
		if len(m.items) > 0 {
			m.confirm = true
			item := m.items[m.cursor]
			m.message = fmt.Sprintf("Kill %s (port %s, PID %s)? [y/N]", item.Process, item.Port, item.PID)
		}
	}

	return m, nil
}

func (m model) View() string {
	s := "🔥 Linux Local Port Killer (TUI)\n"
	s += "↑/↓ atau j/k: navigasi | Enter: kill | r: refresh | q: keluar\n\n"

	if len(m.items) == 0 {
		s += "Tidak ada port LISTEN yang aktif, atau butuh hak akses root.\n"
	}

	for i, item := range m.items {
		cursor := "  "
		if m.cursor == i {
			cursor = "👉"
		}
		s += fmt.Sprintf("%s Port: %-6s | Process: %-15s | PID: %s\n",
			cursor, item.Port, item.Process, item.PID)
	}

	if m.message != "" {
		s += fmt.Sprintf("\n[STATUS]: %s\n", m.message)
	}

	return s
}

func main() {
	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Terjadi error: %v\n", err)
		os.Exit(1)
	}
}
