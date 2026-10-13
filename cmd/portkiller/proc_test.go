package main

import (
	"os"
	"reflect"
	"testing"
)

func TestParseCmdline(t *testing.T) {
	got := parseCmdline([]byte("node\x00server.js\x00--port\x003000\x00"))
	want := []string{"node", "server.js", "--port", "3000"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	if parseCmdline(nil) != nil {
		t.Fatal("nil should give nil")
	}
	if parseCmdline([]byte{}) != nil {
		t.Fatal("empty should give nil")
	}
	got = parseCmdline([]byte("a\x00\x00b\x00"))
	want = []string{"a", "", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("empty arg: got %q want %q", got, want)
	}
}

func TestFindProjectRoot(t *testing.T) {
	cases := []struct {
		name   string
		files  map[string]bool
		path   string
		home   string
		expect string
	}{
		{"gomod", map[string]bool{"/home/u/app/go.mod": true}, "/home/u/app/sub", "", "app"},
		{"git", map[string]bool{"/srv/x/.git": true}, "/srv/x/a/b", "", "x"},
		{"composer", map[string]bool{"/srv/y/composer.json": true}, "/srv/y/public", "", "y"},
		{"package", map[string]bool{"/srv/z/package.json": true}, "/srv/z/src", "", "z"},
		{"fallback", map[string]bool{}, "/home/u/none", "", "none"},
		{"root", map[string]bool{}, "/", "", "/"},
		{"home-stop", map[string]bool{"/home/ubai02/.git": true}, "/home/ubai02/sideproj", "/home/ubai02", "sideproj"},
		{"wrong-home-documented", map[string]bool{"/home/ubai02/.git": true}, "/home/ubai02/sideproj", "/root", "ubai02"},
		{"home-nested", map[string]bool{"/home/ubai02/.git": true, "/home/ubai02/sideproj/go.mod": true}, "/home/ubai02/sideproj/sub", "/home/ubai02", "sideproj"},
	}
	for _, c := range cases {
		exists := func(p string) bool { return c.files[p] }
		if got := findProjectRootFrom(c.path, exists, c.home); got != c.expect {
			t.Errorf("%s: got %q want %q", c.name, got, c.expect)
		}
	}
}

func TestOwnerHomeSelf(t *testing.T) {
	if os.Getenv("SUDO_USER") != "" {
		t.Skip("dijalankan via sudo")
	}
	want, _ := os.UserHomeDir()
	if got := ownerHome(os.Getpid()); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestLookupHomeForUid(t *testing.T) {
	byID := func(id string) (string, bool) {
		if id == "0" {
			return "/root", true
		}
		if id == "1000" {
			return "/home/ubai02", true
		}
		return "", false
	}
	byName := func(n string) (string, bool) {
		if n == "ubai02" {
			return "/home/ubai02", true
		}
		return "", false
	}
	// uid 0 + SUDO_USER -> home sudo user, bukan /root
	if h, _ := lookupHomeForUid("0", "ubai02", byID, byName); h != "/home/ubai02" {
		t.Fatalf("root+sudo: got %q", h)
	}
	// uid biasa -> home sendiri
	if h, _ := lookupHomeForUid("1000", "", byID, byName); h != "/home/ubai02" {
		t.Fatalf("uid: got %q", h)
	}
}

func TestNormalizeProjectName(t *testing.T) {
	for in, want := range map[string]string{"": "-", ".": "-", "/": "-", "app": "app"} {
		if got := normalizeProjectName(in); got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

func TestEnrichProjectDocker(t *testing.T) {
	it := PortItem{Process: "docker:web", PID: "abc123", Source: "docker"}
	enrichProject(&it)
	if it.Project != "web" || it.Cwd != "-" {
		t.Fatalf("got %+v", it)
	}
}

func TestParseStat(t *testing.T) {
	// pid 123, comm "my (app) x", ppid 1, tty_nr 0
	raw := "123 (my (app) x) S 1 123 123 0 -1 0 0 0 0 0 0 0 0 0 0 20 0 1 0 0 0 0"
	ppid, tty, err := parseStat([]byte(raw))
	if err != nil || ppid != 1 || tty != 0 {
		t.Fatalf("got %d %d %v", ppid, tty, err)
	}
	if _, _, err := parseStat([]byte("no-parens here")); err == nil {
		t.Fatal("expected error")
	}
}

func TestIsOrphan(t *testing.T) {
	cases := []struct {
		name       string
		ppid       int
		parentComm string
		tty        int
		proc       string
		uid        int
		proto      string
		project    string
		want       bool
	}{
		{"ppid1", 1, "init", 0, "node", 1000, "tcp", "app", true},
		{"subreaper", 2345, "systemd", 0, "node", 1000, "tcp", "app", true},
		{"has-tty", 1, "init", 34816, "node", 1000, "tcp", "app", false},
		{"system-name", 1, "init", 0, "sshd", 1000, "tcp", "app", false},
		{"service-uid", 1, "init", 0, "daemon", 100, "tcp", "app", false},
		{"root-nonsystem", 1, "init", 0, "node", 0, "tcp", "app", true},
		{"normal", 5000, "bash", 34816, "node", 1000, "tcp", "app", false},
		{"udp-chromium", 1, "systemd", 0, "chromium", 1000, "udp", "ubai02", false},
		{"tcp-no-project", 1, "init", 0, "node", 1000, "tcp", "-", false},
		{"tcp-empty-project", 1, "init", 0, "node", 1000, "tcp", "", false},
	}
	for _, c := range cases {
		if got := isOrphan(c.ppid, c.parentComm, c.tty, c.proc, c.uid, c.proto, c.project); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
