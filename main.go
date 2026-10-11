package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type mode int

const (
	modeNormal mode = iota
	modeFilter
	modeConfirm
)

var sortNames = []string{"PORT", "PROCESS", "PID", "PROTO"}

// ---------- Styles ----------
var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("2"))
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("2"))
	selStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6"))
	dockerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	sysStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	normalStyle = lipgloss.NewStyle()
	keyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6"))
	warnStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

var systemProcs = map[string]bool{
	"containerd": true, "systemd-resolve": true, "systemd": true,
	"sshd": true, "dockerd": true, "NetworkManager": true, "cupsd": true,
}

// ---------- Messages ----------
type tickMsg time.Time
type portsMsg []PortItem
type actionMsg struct {
	item  PortItem
	force bool
	err   error
}

func refreshCmd() tea.Msg { return portsMsg(GetActivePorts()) }

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func killCmd(item PortItem, force bool) tea.Cmd {
	return func() tea.Msg {
		var err error
		if strings.HasPrefix(item.Process, "docker:") {
			err = StopContainer(item.PID)
		} else {
			err = KillProcess(item.PID, force)
		}
		return actionMsg{item: item, force: force, err: err}
	}
}

// ---------- Model ----------
type model struct {
	all     []PortItem
	view    []PortItem
	cursor  int
	offset  int
	filter  string
	mode    mode
	sortBy  int
	message string
	isErr   bool
	target  PortItem
	force   bool
	busy    bool
	width   int
	height  int
}

func initialModel() model {
	m := model{width: 80, height: 24}
	m.all = GetActivePorts()
	m.rebuild()
	return m
}

func (m model) Init() tea.Cmd { return tickCmd() }

func (m *model) rebuild() {
	f := strings.ToLower(m.filter)
	v := make([]PortItem, 0, len(m.all))
	for _, it := range m.all {
		if f == "" || strings.Contains(strings.ToLower(it.Port+" "+it.Process+" "+it.PID+" "+it.Addr+" "+it.Proto), f) {
			v = append(v, it)
		}
	}
	sort.SliceStable(v, func(i, j int) bool {
		switch m.sortBy {
		case 1:
			return v[i].Process < v[j].Process
		case 2:
			a, _ := strconv.Atoi(v[i].PID)
			b, _ := strconv.Atoi(v[j].PID)
			return a < b
		case 3:
			return v[i].Proto < v[j].Proto
		default:
			a, _ := strconv.Atoi(v[i].Port)
			b, _ := strconv.Atoi(v[j].Port)
			return a < b
		}
	})
	m.view = v
	m.clamp()
}

func (m model) rows() int {
	r := m.height - 4 // title + header + footer + status
	if r < 1 {
		r = 1
	}
	return r
}

func (m *model) clamp() {
	if m.cursor >= len(m.view) {
		m.cursor = len(m.view) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	r := m.rows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+r {
		m.offset = m.cursor - r + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *model) askKill(force bool) {
	if len(m.view) == 0 {
		return
	}
	m.target = m.view[m.cursor]
	m.force = force
	m.mode = modeConfirm
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clamp()

	case tickMsg:
		return m, tea.Batch(refreshCmd, tickCmd())

	case portsMsg:
		m.all = []PortItem(msg)
		m.rebuild()

	case actionMsg:
		m.busy = false
		if msg.err != nil {
			m.message, m.isErr = fmt.Sprintf("Gagal %s (port %s): %v", msg.item.Process, msg.item.Port, msg.err), true
		} else {
			m.message, m.isErr = fmt.Sprintf("Berhasil menghentikan %s (port %s)", msg.item.Process, msg.item.Port), false
		}
		return m, refreshCmd

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch m.mode {
	case modeConfirm:
		m.mode = modeNormal
		if key == "y" || key == "Y" {
			m.busy = true
			m.message, m.isErr = "Memproses...", false
			return m, killCmd(m.target, m.force)
		}
		m.message, m.isErr = "Dibatalkan.", false
		return m, nil

	case modeFilter:
		switch key {
		case "enter":
			m.mode = modeNormal
		case "esc":
			m.mode = modeNormal
			m.filter = ""
		case "backspace":
			if len(m.filter) > 0 {
				m.filter = m.filter[:len(m.filter)-1]
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
	case "r", "f5":
		return m, refreshCmd
	case "s", "f6":
		m.sortBy = (m.sortBy + 1) % len(sortNames)
		m.rebuild()
	case "enter", "f9":
		if !m.busy {
			m.askKill(false)
		}
	case "K", "f8":
		if !m.busy {
			m.askKill(true)
		}
	}
	m.clamp()
	return m, nil
}

// ---------- View ----------
func pad(s string, w int) string {
	if len(s) >= w {
		return s[:w]
	}
	return s + strings.Repeat(" ", w-len(s))
}

func (m model) fullWidth(st lipgloss.Style, s string) string {
	return st.Width(m.width).MaxWidth(m.width).Render(s)
}

func footerItem(k, l string) string {
	return keyStyle.Render(k) + labelStyle.Render(pad(l, len(l)))
}

func (m model) View() string {
	var b strings.Builder

	// Title bar
	root := ""
	if os.Geteuid() != 0 {
		root = "  [bukan root: sebagian proses tidak terbaca]"
	}
	title := fmt.Sprintf(" PORT KILLER  |  %d port  |  sort: %s  |  filter: %q%s",
		len(m.view), sortNames[m.sortBy], m.filter, root)
	b.WriteString(m.fullWidth(titleStyle, title) + "\n")

	// Column header
	head := fmt.Sprintf(" %-6s %-16s %-7s %-8s %s", "PROTO", "ADDRESS", "PORT", "PID", "PROCESS")
	b.WriteString(m.fullWidth(headerStyle, head) + "\n")

	// Rows
	r := m.rows()
	end := m.offset + r
	if end > len(m.view) {
		end = len(m.view)
	}
	drawn := 0
	for i := m.offset; i < end; i++ {
		it := m.view[i]
		pid := it.PID
		if len(pid) > 8 {
			pid = pid[:8]
		}
		line := fmt.Sprintf(" %-6s %-16s %-7s %-8s %s", it.Proto, pad(it.Addr, 16), it.Port, pid, it.Process)

		st := normalStyle
		switch {
		case strings.HasPrefix(it.Process, "docker:"):
			st = dockerStyle
		case systemProcs[it.Process]:
			st = sysStyle
		}
		if i == m.cursor {
			st = selStyle
		}
		b.WriteString(m.fullWidth(st, line) + "\n")
		drawn++
	}
	if len(m.view) == 0 {
		b.WriteString(" Tidak ada port yang cocok / butuh hak akses root.\n")
		drawn++
	}
	for ; drawn < r; drawn++ {
		b.WriteString("\n")
	}

	// Status line
	switch {
	case m.mode == modeConfirm:
		verb := "Stop"
		if m.force {
			verb = "KILL -9"
		}
		warn := ""
		if systemProcs[m.target.Process] {
			warn = " [PROSES SISTEM!]"
		}
		b.WriteString(m.fullWidth(warnStyle, fmt.Sprintf(" %s %s (port %s, PID %s)?%s [y/N]", verb, m.target.Process, m.target.Port, m.target.PID, warn)) + "\n")
	case m.mode == modeFilter:
		b.WriteString(m.fullWidth(warnStyle, " Cari: "+m.filter+"█  (Enter: terapkan, Esc: batal)") + "\n")
	case m.message != "":
		st := okStyle
		if m.isErr {
			st = errStyle
		}
		b.WriteString(st.Render(" "+m.message) + "\n")
	default:
		b.WriteString("\n")
	}

	// Footer (gaya htop)
	foot := strings.Join([]string{
		footerItem("F3", "Cari "),
		footerItem("F5", "Refresh "),
		footerItem("F6", "Urut "),
		footerItem("F8", "Force "),
		footerItem("F9", "Kill "),
		footerItem("F10", "Keluar "),
	}, "")
	b.WriteString(foot)

	return b.String()
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Terjadi error: %v\n", err)
		os.Exit(1)
	}
}
