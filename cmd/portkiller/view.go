package main

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"os"
	"strconv"
	"strings"
	"time"
)

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

// rowText builds one table row; optional columns are hidden on narrow terminals.
func (m model) rowText(mark, proto, addr, port, pid, uptime, project, health, proc string) string {
	b := " " + mark + pad(proto, 6) + " " + pad(addr, 16) + " " + pad(port, 7) + " " + pad(pid, 8)
	if m.width >= 78 {
		b += " " + pad(health, 16)
	}
	if m.width >= 100 {
		b += " " + pad(uptime, 12)
	}
	if m.width >= 120 {
		b += " " + pad(project, 18)
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
		return "○ no response"
	case HealthNotApplicable:
		return "-"
	default:
		return "…"
	}
}

func (m model) View() string {
	if m.showHistory {
		var hb strings.Builder
		hb.WriteString(m.fullWidth(headerStyle, " PORT HISTORY  (press any key to close)") + "\n")
		if len(m.events) == 0 {
			hb.WriteString(m.fullWidth(normalStyle, " No events recorded yet.") + "\n")
		}
		for i, e := range m.events {
			if i >= m.height-3 {
				break
			}
			hb.WriteString(m.fullWidth(normalStyle, " "+formatEvent(e)) + "\n")
		}
		return hb.String()
	}
	if m.showHelp {
		return m.helpView()
	}
	var b strings.Builder

	// Column header (first column = 1-rune orphan marker + PROTO)
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
		b.WriteString(" No matching ports / root access may be required.\n")
		drawn++
	}
	for ; drawn < r; drawn++ {
		b.WriteString("\n")
	}

	if m.showDetail {
		var dl []string
		if len(m.view) > 0 {
			dl = detailLines(m.view[m.cursor], m.detail)
		}
		for i := 0; i < detailHeight; i++ {
			l := ""
			if i < len(dl) {
				l = dl[i]
			}
			b.WriteString(m.fullWidth(detailStyle, l) + "\n")
		}
	}

	// Baris status / konfirmasi / filter
	switch {
	case m.mode == modeConfirmProject:
		h, d := projectSummary(m.ptarget)
		b.WriteString(m.fullWidth(warnStyle, fmt.Sprintf(" Stop project %s: %d processes, %d containers (SIGTERM/docker stop): %s? [y/N]", m.pname, h, d, projectNames(m.ptarget, 3))) + "\n")
	case m.mode == modeConfirmOrphans:
		names := make([]string, 0, len(m.otarget))
		for _, it := range m.otarget {
			names = append(names, it.Process+"(:"+it.Port+")")
		}
		b.WriteString(m.fullWidth(warnStyle, fmt.Sprintf(" Clean up %d orphans (SIGTERM, all ports): %s? [y/N]", len(m.otarget), strings.Join(names, ", "))) + "\n")
	case m.mode == modeConfirm:
		verb := "Stop"
		if m.force {
			verb = "KILL -9"
		}
		warn := ""
		if systemProcs[m.target.Process] {
			warn = " [SYSTEM PROCESS!]"
		}
		b.WriteString(m.fullWidth(warnStyle, fmt.Sprintf(" %s %s (:%s, PID %s)? %s.%s [y/N]", verb, m.target.Process, m.target.Port, m.target.PID, blastText(m.blast, m.bLoad), warn)) + "\n")
	case m.mode == modeFilter:
		b.WriteString(m.fullWidth(warnStyle, " Search: "+m.filter+"█  (Enter: apply, Esc: cancel)") + "\n")
	case m.message != "":
		st := okStyle
		if m.isErr {
			st = errStyle
		}
		b.WriteString(st.Render(" "+m.message) + "\n")
	default:
		if len(m.violations) > 0 {
			b.WriteString(m.fullWidth(warnStyle, violationText(m.violations)) + "\n")
		} else {
			b.WriteString("\n")
		}
	}

	// Info bar
	root := ""
	if os.Geteuid() != 0 {
		root = " | not root"
	}
	nOrphan := len(orphanItems(m.all))
	nOrphanView := len(orphanItems(m.view))
	orphanFlag := ""
	if m.orphans {
		orphanFlag = " | orphan-only"
	} else if nOrphan != nOrphanView {
		orphanFlag = " | orphans hidden by filter"
	}
	info := fmt.Sprintf(" %d port | orphan: %d%s | proto: %s | sort: %s | filter: %q | up: %s%s",
		len(m.view), nOrphan, orphanFlag, protoNames[m.proto], sortNames[m.sortBy], m.filter, sysUptime(), root)
	b.WriteString(m.fullWidth(titleStyle, info) + "\n")

	// Footer
	var fb strings.Builder
	for _, f := range footerKeys {
		fb.WriteString(footerItem(f.key, f.label))
	}
	b.WriteString(lipgloss.NewStyle().MaxWidth(m.width).Render(fb.String()))

	return b.String()
}
