package main

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const detailHeight = 5

var detailStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))

type Detail struct {
	Cmdline string
	Cwd     string
	User    string
	Parent  string
	Mem     string
	Threads string
}

// parseStatus murni: isi /proc/<pid>/status -> map key:value.
func parseStatus(b []byte) map[string]string {
	out := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		i := strings.Index(l, ":")
		if i < 0 {
			continue
		}
		out[l[:i]] = strings.TrimSpace(l[i+1:])
	}
	return out
}

func firstField(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// formatKB murni: "20480 kB" -> "20.0 MB".
func formatKB(s string) string {
	n, err := strconv.Atoi(firstField(s))
	if err != nil {
		return "-"
	}
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f GB", float64(n)/1024/1024)
	case n >= 1024:
		return fmt.Sprintf("%.1f MB", float64(n)/1024)
	}
	return fmt.Sprintf("%d kB", n)
}

func readDetail(it PortItem) Detail {
	d := Detail{Cmdline: "-", Cwd: "-", User: "-", Parent: "-", Mem: "-", Threads: "-"}
	if it.IsDocker() {
		d.Cmdline = "container " + strings.TrimPrefix(it.Process, "docker:") + " (" + it.PID + ")"
		return d
	}
	pid, err := strconv.Atoi(it.PID)
	if err != nil {
		return d
	}
	if b, err := os.ReadFile("/proc/" + it.PID + "/cmdline"); err == nil {
		if a := parseCmdline(b); len(a) > 0 {
			d.Cmdline = strings.Join(a, " ")
		}
	}
	if cwd, err := readCwd(pid); err == nil && cwd != "" {
		d.Cwd = cwd
	}
	if b, err := os.ReadFile("/proc/" + it.PID + "/status"); err == nil {
		st := parseStatus(b)
		if uid := firstField(st["Uid"]); uid != "" {
			d.User = uid
			if u, err := user.LookupId(uid); err == nil {
				d.User = u.Username
			}
		}
		if pp := firstField(st["PPid"]); pp != "" {
			d.Parent = pp
			if n, err := strconv.Atoi(pp); err == nil {
				if c := parentCommOf(n); c != "" {
					d.Parent = pp + " (" + c + ")"
				}
			}
		}
		d.Mem = formatKB(st["VmRSS"])
		if t := st["Threads"]; t != "" {
			d.Threads = t
		}
	}
	return d
}

// detailLines murni: selalu detailHeight baris.
func detailLines(it PortItem, d Detail) []string {
	return []string{
		fmt.Sprintf(" DETAIL  %s  :%s  PID %s", it.Process, it.Port, it.PID),
		" Cmd     : " + d.Cmdline,
		" Cwd     : " + d.Cwd,
		" User    : " + d.User + "   Induk: " + d.Parent,
		" Memori  : " + d.Mem + "   Thread: " + d.Threads + "   Bind: " + it.Addr + "   Proyek: " + it.Project,
	}
}

func helpLines() []string {
	return []string{
		" j/k ↑/↓ PgUp/PgDn g/G   Navigasi",
		" F3 atau /               Cari (Esc hapus filter)",
		" F4 atau t               Proto TCP / UDP / ALL",
		" F5 atau r               Refresh manual (auto 2 detik)",
		" F6 atau s               Urutan PORT / PROCESS / PID / PROTO",
		" F9 atau Enter           Stop (SIGTERM / docker stop) + dampak",
		" F8 atau K               Force kill (SIGKILL)",
		" i atau Tab              Panel detail baris terpilih",
		" h                       Riwayat port muncul/hilang + penyebab",
		"o                       Filter orphan saja",
		" X                       Bersihkan semua orphan (SIGTERM)",
		" P                       Stop semua proses proyek baris terpilih",
		" R                       Jalankan ulang proses yang terakhir dihentikan",
		" ? atau F1               Bantuan ini",
		" F10 atau q              Keluar",
		"",
		" Marker  ⚠ orphan (TCP, tanpa tty, punya proyek)",
		" Health  ● hidup   ○ tidak merespons   … belum dicek   - UDP",
	}
}

func (m model) helpView() string {
	var b strings.Builder
	b.WriteString(m.fullWidth(headerStyle, " BANTUAN  (tombol apa saja untuk menutup)") + "\n")
	for _, l := range helpLines() {
		b.WriteString(m.fullWidth(normalStyle, l) + "\n")
	}
	return b.String()
}

func (m *model) refreshDetail() {
	if !m.showDetail || len(m.view) == 0 {
		return
	}
	m.detail = readDetail(m.view[m.cursor])
}

func (m model) detailRows() int {
	if m.showDetail {
		return detailHeight
	}
	return 0
}
