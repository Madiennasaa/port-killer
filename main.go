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
	modeConfirmOrphans
)

var sortNames = []string{"PORT", "PROCESS", "PID", "PROTO"}
var protoNames = []string{"TCP", "UDP", "ALL"}

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
	orphanStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
)

var systemProcs = map[string]bool{
	"containerd": true, "systemd-resolve": true, "systemd": true,
	"sshd": true, "dockerd": true, "NetworkManager": true, "cupsd": true,
	"avahi-daemon": true, "chronyd": true, "dbus-daemon": true,
	"bluetoothd": true, "wpa_supplicant": true,
}

// ---------- Messages ----------
type tickMsg time.Time
type portsMsg []PortItem

const msgHealthPanic = "Health check panic, dilewati siklus ini."

type actionMsg struct {
	item  PortItem
	force bool
	err   error
}
type orphansMsg struct {
	done int
	err  error
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

// ---------- Model ----------
type model struct {
	all     []PortItem
	view    []PortItem
	cursor  int
	offset  int
	filter  string
	mode    mode
	sortBy  int
	proto   int
	message string
	isErr   bool
	target  PortItem
	force   bool
	busy    bool
	width   int
	height  int
	health  map[string]healthResult
	hBusy   bool
	orphans bool
	otarget []PortItem
}

func initialModel() model {
	m := model{width: 80, height: 24, health: map[string]healthResult{}}
	m.all = GetActivePorts()
	m.applyHealth()
	m.rebuild()
	return m
}

func (m *model) applyHealth() {
	if m.health == nil {
		m.health = map[string]healthResult{}
	}
	for i := range m.all {
		if r, ok := m.health[healthKey(m.all[i].Proto, m.all[i].Addr, m.all[i].Port, m.all[i].PID)]; ok {
			m.all[i].Health, m.all[i].Latency = r.status, r.latency
		} else if strings.HasPrefix(m.all[i].Proto, "udp") {
			m.all[i].Health = HealthNotApplicable
		} else {
			m.all[i].Health = HealthUnknown
		}
	}
}

func (m model) Init() tea.Cmd { return tickCmd() }

func (m *model) rebuild() {
	f := strings.ToLower(m.filter)
	v := make([]PortItem, 0, len(m.all))
	for _, it := range m.all {
		if m.orphans && !it.Orphan {
			continue
		}
		if m.proto == 0 && !strings.HasPrefix(it.Proto, "tcp") {
			continue
		}
		if m.proto == 1 && !strings.HasPrefix(it.Proto, "udp") {
			continue
		}
		hay := it.Port + " " + it.Process + " " + it.PID + " " + it.Addr + " " + it.Proto + " " + it.Project
		if it.Cwd != "-" && it.Cwd != "" {
			hay += " " + it.Cwd
		}
		if f == "" || strings.Contains(strings.ToLower(hay), f) {
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
	r := m.height - 4 // header + status + info + footer
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

func orphanItems(all []PortItem) []PortItem {
	seen := map[string]bool{}
	var out []PortItem
	for _, it := range all {
		if !it.Orphan || it.IsDocker() || seen[it.PID] {
			continue
		}
		seen[it.PID] = true
		out = append(out, it)
	}
	return out
}

func (m *model) askCleanOrphans() {
	m.otarget = orphanItems(m.all)
	if len(m.otarget) == 0 {
		m.message, m.isErr = "Tidak ada orphan.", false
		return
	}
	m.mode = modeConfirmOrphans
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
		// Buang hasil usang agar PID reuse tidak mewarisi status lama.
		if m.health != nil {
			alive := make(map[string]healthResult, len(m.all))
			for _, it := range m.all {
				k := healthKey(it.Proto, it.Addr, it.Port, it.PID)
				if r, ok := m.health[k]; ok {
					alive[k] = r
				}
			}
			m.health = alive
		}
		m.applyHealth()
		m.rebuild()
		if !m.hBusy {
			m.hBusy = true
			return m, healthCmd(m.all)
		}
		return m, nil

	case healthResultMsg:
		m.hBusy = false
		if m.health == nil {
			m.health = map[string]healthResult{}
		}
		for k, r := range msg.results {
			m.health[k] = r
		}
		if msg.panicked {
			m.message, m.isErr = msgHealthPanic, true
		} else if m.message == msgHealthPanic {
			m.message, m.isErr = "", false
		}
		m.applyHealth()
		m.rebuild()
		return m, nil

	case actionMsg:
		m.busy = false
		if msg.err != nil {
			m.message, m.isErr = fmt.Sprintf("Gagal %s (port %s): %v", msg.item.Process, msg.item.Port, msg.err), true
		} else {
			m.message, m.isErr = fmt.Sprintf("Berhasil menghentikan %s (port %s)", msg.item.Process, msg.item.Port), false
		}
		return m, refreshCmd

	case orphansMsg:
		m.busy = false
		if msg.err != nil {
			m.message, m.isErr = fmt.Sprintf("Bersihkan orphan: %d OK, err: %v", msg.done, msg.err), true
		} else {
			m.message, m.isErr = fmt.Sprintf("Bersihkan orphan: %d proses (SIGTERM)", msg.done), false
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
	case modeConfirmOrphans:
		m.mode = modeNormal
		if key == "y" || key == "Y" {
			m.busy = true
			m.message, m.isErr = "Membersihkan orphan (SIGTERM)...", false
			return m, killOrphansCmd(m.otarget)
		}
		m.message, m.isErr = "Dibatalkan.", false
		return m, nil

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
		}
	case "K", "f8":
		if !m.busy {
			m.askKill(true)
		}
	case "o":
		m.orphans = !m.orphans
		m.rebuild()
	case "X":
		if !m.busy {
			m.askCleanOrphans()
		}
	}
	m.clamp()
	return m, nil
}

// ---------- View ----------
func pad(s string, w int) string {
	r := []rune(s)
	if len(r) > w {
		if w <= 1 {
			return string(r[:w])
		}
		return string(r[:w-1]) + "…"
	}
	return s + strings.Repeat(" ", w-len(r))
}

func truncRunes(s string, w int) string {
	r := []rune(s)
	if w > 0 && len(r) > w {
		return string(r[:w])
	}
	return s
}

func (m model) fullWidth(st lipgloss.Style, s string) string {
	s = truncRunes(strings.ReplaceAll(s, "\n", " "), m.width)
	return st.Width(m.width).MaxWidth(m.width).Inline(true).Render(s)
}

// rowText menyusun satu baris tabel; kolom opsional disembunyikan saat terminal sempit.
func (m model) rowText(mark, proto, addr, port, pid, uptime, project, health, proc string) string {
	b := " " + mark + pad(proto, 6) + " " + pad(addr, 16) + " " + pad(port, 7) + " " + pad(pid, 8)
	if m.width >= 100 {
		b += " " + pad(uptime, 12)
	}
	if m.width >= 120 {
		b += " " + pad(project, 18)
	}
	if m.width >= 90 {
		b += " " + pad(health, 16)
	}
	return b + " " + proc
}

func footerItem(k, l string) string {
	return keyStyle.Render(k) + labelStyle.Render(pad(l, len(l)))
}

func sysUptime() string {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "?"
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return "?"
	}
	s, _ := strconv.ParseFloat(f[0], 64)
	t := int(s)
	return fmt.Sprintf("%dd %02dh %02dm", t/86400, t%86400/3600, t%3600/60)
}

func healthText(it PortItem) string {
	switch it.Health {
	case HealthAlive:
		if it.Latency < time.Millisecond {
			return "● <1ms"
		}
		return "● " + it.Latency.Truncate(time.Millisecond).String()
	case HealthDead:
		return "○ tidak merespons"
	case HealthNotApplicable:
		return "-"
	default:
		return "…"
	}
}

func (m model) View() string {
	var b strings.Builder

	// Header kolom (kolom pertama = marker orphan 1 rune + PROTO)
	head := m.rowText(" ", "PROTO", "ADDRESS", "PORT", "PID", "UPTIME", "PROJECT", "HEALTH", "PROCESS")
	b.WriteString(m.fullWidth(headerStyle, head) + "\n")

	// Baris
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
		mark := " "
		if it.Orphan {
			mark = "⚠"
		}
		line := m.rowText(mark, it.Proto, it.Addr, it.Port, pid, it.Uptime, it.Project, healthText(it), it.Process)

		st := normalStyle
		switch {
		case it.Orphan:
			st = orphanStyle
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

	// Baris status / konfirmasi / filter
	switch {
	case m.mode == modeConfirmOrphans:
		names := make([]string, 0, len(m.otarget))
		for _, it := range m.otarget {
			names = append(names, it.Process+"(:"+it.Port+")")
		}
		b.WriteString(m.fullWidth(warnStyle, fmt.Sprintf(" Bersihkan %d orphan (SIGTERM, semua port): %s? [y/N]", len(m.otarget), strings.Join(names, ", "))) + "\n")
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

	// Info bar
	root := ""
	if os.Geteuid() != 0 {
		root = " | bukan root"
	}
	nOrphan := len(orphanItems(m.all))
	nOrphanView := len(orphanItems(m.view))
	orphanFlag := ""
	if m.orphans {
		orphanFlag = " | orphan-only"
	} else if nOrphan != nOrphanView {
		orphanFlag = " | orphan tersembunyi filter"
	}
	info := fmt.Sprintf(" %d port | orphan: %d%s | proto: %s | sort: %s | filter: %q | up: %s%s",
		len(m.view), nOrphan, orphanFlag, protoNames[m.proto], sortNames[m.sortBy], m.filter, sysUptime(), root)
	b.WriteString(m.fullWidth(titleStyle, info) + "\n")

	// Footer
	b.WriteString(strings.Join([]string{
		footerItem("F3", "Cari "),
		footerItem("F4", "Proto "),
		footerItem("F5", "Refresh "),
		footerItem("F6", "Urut "),
		footerItem("F8", "Force "),
		footerItem("F9", "Kill "),
		footerItem("o", "Orphan "),
		footerItem("X", "Bersih "),
		footerItem("F10", "Keluar "),
	}, ""))

	return b.String()
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Terjadi error: %v\n", err)
		os.Exit(1)
	}
}
