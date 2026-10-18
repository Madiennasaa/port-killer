package main

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type mode int

const (
	modeNormal mode = iota
	modeFilter
	modeConfirm
	modeConfirmOrphans
	modeConfirmProject
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

// ---------- Model ----------
type model struct {
	helpOverride []string
	guard        []Rule
	violations   []Violation
	guardSeen    map[string]bool
	restarts     []Restartable
	lastMsg      string
	seeded       bool
	cause        string
	causeAt      time.Time
	showHistory  bool
	events       []Event
	all          []PortItem
	view         []PortItem
	cursor       int
	offset       int
	filter       string
	mode         mode
	sortBy       int
	proto        int
	message      string
	isErr        bool
	target       PortItem
	force        bool
	busy         bool
	width        int
	height       int
	health       map[string]healthResult
	hBusy        bool
	orphans      bool
	otarget      []PortItem
	blast        Blast
	bLoad        bool

	showDetail bool
	showHelp   bool
	msgAt      time.Time
	ptarget    []PortItem
	pname      string
	detail     Detail
}

func initialModel() model {
	m := model{width: 80, height: 24, health: map[string]healthResult{}}
	m.all = GetActivePorts()
	m.guard = loadGuard()
	m.guardSeen = map[string]bool{}
	m.checkGuardNow()
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

// protoMatches reports whether an item passes the current protocol filter
// (0=TCP only, 1=UDP only, 2=ALL).
func (m model) protoMatches(it PortItem) bool {
	switch m.proto {
	case 0:
		return strings.HasPrefix(it.Proto, "tcp")
	case 1:
		return strings.HasPrefix(it.Proto, "udp")
	}
	return true
}

// matchesFilter reports whether the item passes the orphan filter and the
// free-text search (which also covers the project cwd).
func (m model) matchesFilter(it PortItem, lowerFilter string) bool {
	if m.orphans && !it.Orphan {
		return false
	}
	if lowerFilter == "" {
		return true
	}
	hay := it.Port + " " + it.Process + " " + it.PID + " " + it.Addr + " " + it.Proto + " " + it.Project
	if it.Cwd != "-" && it.Cwd != "" {
		hay += " " + it.Cwd
	}
	return strings.Contains(strings.ToLower(hay), lowerFilter)
}

// sortNum is a numeric sort key for a pid/port field. A non-numeric value
// (a docker container id, a dash) sorts as 0, which is fine for display order.
func sortNum(s string) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return 0
}

// sortView orders the visible rows by the active sort mode (see sortNames).
func sortView(v []PortItem, sortBy int) {
	sort.SliceStable(v, func(i, j int) bool {
		switch sortBy {
		case 1:
			return v[i].Process < v[j].Process
		case 2:
			return sortNum(v[i].PID) < sortNum(v[j].PID)
		case 3:
			return v[i].Proto < v[j].Proto
		default:
			return sortNum(v[i].Port) < sortNum(v[j].Port)
		}
	})
}

func (m *model) rebuild() {
	f := strings.ToLower(m.filter)
	v := make([]PortItem, 0, len(m.all))
	for _, it := range m.all {
		if !m.protoMatches(it) || !m.matchesFilter(it, f) {
			continue
		}
		v = append(v, it)
	}
	sortView(v, m.sortBy)
	m.view = v
	m.clamp()
}

func (m model) rows() int {
	r := m.height - 4 - m.detailRows() // header + status + info + footer
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
	m.bLoad = true
	m.blast = Blast{}
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

func (m *model) askStopProject() {
	if len(m.view) == 0 {
		return
	}
	name := m.view[m.cursor].Project
	for _, g := range groupByProject(m.all) {
		if g.Project != name {
			continue
		}
		t := projectTargets(g, procUid)
		if len(t) == 0 {
			m.message, m.isErr = "Project "+name+": no safe targets.", false
			return
		}
		m.ptarget, m.pname = t, name
		m.mode = modeConfirmProject
		return
	}
	m.message, m.isErr = "This row has no project.", false
}

func (m *model) askCleanOrphans() {
	m.otarget = orphanItems(m.all)
	if len(m.otarget) == 0 {
		m.message, m.isErr = "No orphans.", false
		return
	}
	m.mode = modeConfirmOrphans
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	nm, cmd := m.update(msg)
	if mm, ok := nm.(model); ok && mm.message != mm.lastMsg {
		mm.lastMsg = mm.message
		if mm.message != "" {
			mm.msgAt = time.Now()
		}
		nm = mm
	}
	return nm, cmd
}

func (m *model) checkGuardNow() {
	if len(m.guard) == 0 {
		m.violations = nil
		return
	}
	if m.guardSeen == nil {
		m.guardSeen = map[string]bool{}
	}
	m.violations = checkGuard(m.guard, m.all)
	var evs []Event
	cur := map[string]bool{}
	for _, v := range m.violations {
		k := guardKey(v)
		cur[k] = true
		if !m.guardSeen[k] {
			evs = append(evs, Event{Time: time.Now(), Kind: "guard", Proto: v.Item.Proto, Port: v.Item.Port,
				Process: v.Item.Process, PID: v.Item.PID, Cause: "owned by " + v.Rule.Owner})
		}
	}
	m.guardSeen = cur
	appendEvents(eventsPath(), evs)
}

func (m *model) markCause() { m.cause, m.causeAt = "stopped via portkiller", time.Now() }

func (m *model) recordEvents(cur []PortItem) {
	if m.seeded {
		cause := "unknown"
		if time.Since(m.causeAt) < 20*time.Second {
			cause = m.cause
		}
		evs := diffPorts(m.all, cur, time.Now(), cause)
		var keep []Event
		for _, e := range evs {
			if strings.HasPrefix(e.Proto, "udp") && e.Cause != "stopped via portkiller" {
				continue
			}
			keep = append(keep, e)
		}
		appendEvents(eventsPath(), keep)
	}
	m.seeded = true
}

// pruneStaleHealth drops health results whose pid/port/proto combo is no
// longer present, so PID reuse does not inherit an old status.
func (m *model) pruneStaleHealth() {
	if m.health == nil {
		return
	}
	alive := make(map[string]healthResult, len(m.all))
	for _, it := range m.all {
		k := healthKey(it.Proto, it.Addr, it.Port, it.PID)
		if r, ok := m.health[k]; ok {
			alive[k] = r
		}
	}
	m.health = alive
}

// finishWork clears the busy flag and reports a completed background task.
// Every result message ends the same way: a status line and a refresh.
func (m model) finishWork(okMsg, errMsg string, err error) (model, tea.Cmd) {
	m.busy = false
	m.msgAt = time.Now()
	if err != nil {
		m.message, m.isErr = errMsg, true
	} else {
		m.message, m.isErr = okMsg, false
	}
	return m, refreshCmd
}

// messageExpired reports whether a status message has outlived its TTL and is
// safe to clear: it must exist, the model must be idle, and msgAt must be set.
func (m model) messageExpired() bool {
	return m.message != "" && !m.busy && !m.msgAt.IsZero() && time.Since(m.msgAt) > messageTTL
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clamp()

	case tickMsg:
		if m.messageExpired() {
			m.message, m.isErr, m.msgAt = "", false, time.Time{}
		}
		return m, tea.Batch(refreshCmd, tickCmd())

	case portsMsg:
		m.recordEvents([]PortItem(msg))
		m.all = []PortItem(msg)
		m.checkGuardNow()
		m.pruneStaleHealth()
		m.applyHealth()
		m.rebuild()
		m.refreshDetail()
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

	case blastMsg:
		if m.mode == modeConfirm && msg.pid == m.target.PID && msg.port == m.target.Port {
			m.blast, m.bLoad = msg.blast, false
		}
		return m, nil

	case restartMsg:
		if msg.err != nil {
			return m.finishWork("", fmt.Sprintf("Restart: %d OK, err: %v", msg.done, msg.err), msg.err)
		}
		m.restarts = nil
		return m.finishWork(fmt.Sprintf("Restarted: %d processes", msg.done), "", nil)

	case actionMsg:
		return m.finishWork(
			fmt.Sprintf("Stopped %s (port %s)", msg.item.Process, msg.item.Port),
			fmt.Sprintf("Failed %s (port %s): %v", msg.item.Process, msg.item.Port, msg.err),
			msg.err)

	case projectMsg:
		return m.finishWork(
			fmt.Sprintf("Project %s: %d stopped", msg.project, msg.done),
			fmt.Sprintf("Project %s: %d OK, err: %v", msg.project, msg.done, msg.err),
			msg.err)

	case orphansMsg:
		return m.finishWork(
			fmt.Sprintf("Cleaned orphans: %d processes (SIGTERM)", msg.done),
			fmt.Sprintf("Clean orphans: %d OK, err: %v", msg.done, msg.err),
			msg.err)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

// reexecSudo relaunches the current binary under sudo so every process is
// visible. It only returns when sudo is unavailable or the exec fails.
func reexecSudo() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return
	}
	syscall.Exec(sudo, append([]string{"sudo", "-E", exe}, os.Args[1:]...), os.Environ())
}

// appRun is the non-TUI entry point. It returns handled=true when the args
// were a CLI command (so the caller must not start the TUI).
func appRun(args []string, euid int, run func([]string) bool, reexec func()) (handled bool) {
	if !cliNeedsRoot(args) && run(args) {
		return true
	}
	if euid != 0 {
		reexec()
	}
	return run(args)
}

func main() {
	if appRun(os.Args[1:], os.Geteuid(), runCLI, reexecSudo) {
		return
	}
	p := tea.NewProgram(initialModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
