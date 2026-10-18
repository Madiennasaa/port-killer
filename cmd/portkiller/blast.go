package main

import (
	"os"
	"sort"
	"strconv"
	"strings"
)

// Conn is one connection from /proc/net/tcp(6).
type Conn struct {
	LocalPort  int
	RemotePort int
	State      string // hex, "01" = ESTABLISHED
	Inode      string
}

// Blast summarizes the impact of a kill.
type Blast struct {
	Conns    int
	Children int
	Clients  []string // nama proses klien
}

// parseProcNetTCP (pure): parses /proc/net/tcp or tcp6 contents (header skipped).
func parseProcNetTCP(data []byte) []Conn {
	var out []Conn
	for i, line := range strings.Split(string(data), "\n") {
		if i == 0 {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		lp := hexPort(f[1])
		rp := hexPort(f[2])
		if lp < 0 || rp < 0 {
			continue
		}
		out = append(out, Conn{LocalPort: lp, RemotePort: rp, State: f[3], Inode: f[9]})
	}
	return out
}

func hexPort(addr string) int {
	i := strings.LastIndex(addr, ":")
	if i == -1 {
		return -1
	}
	n, err := strconv.ParseUint(addr[i+1:], 16, 32)
	if err != nil {
		return -1
	}
	return int(n)
}

// countEstablished (pure): ESTABLISHED connections whose local port == port.
func countEstablished(conns []Conn, port int) []Conn {
	var out []Conn
	for _, c := range conns {
		if c.State == "01" && c.LocalPort == port {
			out = append(out, c)
		}
	}
	return out
}

// countChildren (pure): number of processes with ppid == pid from a pid->ppid map.
func countChildren(ppids map[int]int, pid int) int {
	n := 0
	for p, pp := range ppids {
		if pp == pid && p != pid {
			n++
		}
	}
	return n
}

// procDir and readProcFile are seams so the /proc race paths are testable.
var (
	procDir      = "/proc"
	readProcFile = os.ReadFile
)

// readPPIDs reads the ppid of every process from /proc/<pid>/stat.
func readPPIDs() map[int]int {
	out := map[int]int{}
	ents, err := os.ReadDir(procDir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := readProcFile(procDir + "/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		if pp, _, err := parseStat(b); err == nil {
			out[pid] = pp
		}
	}
	return out
}

// inodeOwners memetakan inode socket -> nama proses (butuh akses /proc/<pid>/fd).
func inodeOwners(inodes map[string]bool) map[string]string {
	out := map[string]string{}
	if len(inodes) == 0 {
		return out
	}
	ents, err := os.ReadDir(procDir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		fds, err := os.ReadDir(procDir + "/" + e.Name() + "/fd")
		if err != nil {
			continue
		}
		for _, fd := range fds {
			l, err := os.Readlink("/proc/" + e.Name() + "/fd/" + fd.Name())
			if err != nil || !strings.HasPrefix(l, "socket:[") {
				continue
			}
			ino := strings.TrimSuffix(strings.TrimPrefix(l, "socket:["), "]")
			if inodes[ino] {
				if c, err := os.ReadFile("/proc/" + e.Name() + "/comm"); err == nil {
					out[ino] = strings.TrimSpace(string(c))
				}
			}
		}
	}
	return out
}

// summarizeClients (pure): unique client names, sorted.
func summarizeClients(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// ComputeBlast computes the kill impact for an item. Docker: no /proc counting.
func ComputeBlast(it PortItem) Blast {
	var b Blast
	port, err := strconv.Atoi(it.Port)
	if err != nil {
		return b
	}
	var conns []Conn
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if d, err := os.ReadFile(f); err == nil {
			conns = append(conns, parseProcNetTCP(d)...)
		}
	}
	est := countEstablished(conns, port)
	b.Conns = len(est)
	if it.IsDocker() {
		return b
	}
	inodes := map[string]bool{}
	for _, c := range est {
		inodes[c.Inode] = true
	}
	owners := inodeOwners(inodes)
	var names []string
	for _, c := range est {
		names = append(names, owners[c.Inode])
	}
	b.Clients = summarizeClients(names)
	if pid, err := strconv.Atoi(it.PID); err == nil {
		b.Children = countChildren(readPPIDs(), pid)
	}
	return b
}

// blastText (pure): impact sentence for the dialog.
func blastText(b Blast, loading bool) string {
	if loading {
		return "calculating impact..."
	}
	if b.Conns == 0 && b.Children == 0 {
		return "no impact detected"
	}
	s := "dropping " + strconv.Itoa(b.Conns) + " connections"
	if len(b.Clients) > 0 {
		s += " (" + strings.Join(b.Clients, ", ") + ")"
	}
	return s + ", " + strconv.Itoa(b.Children) + " child processes"
}
