package main

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// Rule: port diharapkan dipakai oleh peowned by tertentu (cocok dengan Process atau Project).
type Rule struct {
	Port  string
	Owner string
}

// parseGuard murni: baris "5432=postgres-dev", "#" komentar, kosong dilewati.
func parseGuard(data string) []Rule {
	var out []Rule
	for _, l := range strings.Split(data, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		i := strings.Index(l, "=")
		if i <= 0 {
			continue
		}
		p, o := strings.TrimSpace(l[:i]), strings.TrimSpace(l[i+1:])
		if p == "" || o == "" {
			continue
		}
		out = append(out, Rule{Port: p, Owner: o})
	}
	return out
}

// ownerMatches murni: owner cocok dengan nama proses (tanpa "docker:") atau project.
func ownerMatches(it PortItem, owner string) bool {
	name := strings.TrimPrefix(it.Process, "docker:")
	o := strings.ToLower(owner)
	return strings.ToLower(name) == o || strings.ToLower(it.Project) == o
}

// Violation: port direbut proses yang bukan pemilik.
type Violation struct {
	Rule Rule
	Item PortItem
}

// checkGuard murni: TCP/UDP yang listen di port aturan oleh bukan pemilik.
func checkGuard(rules []Rule, items []PortItem) []Violation {
	var out []Violation
	for _, r := range rules {
		for _, it := range items {
			if it.Port == r.Port && !ownerMatches(it, r.Owner) {
				out = append(out, Violation{Rule: r, Item: it})
			}
		}
	}
	return out
}

// guardKey: dedup pelanggaran agar riwayat tidak dibanjiri tiap tick.
func guardKey(v Violation) string {
	return v.Rule.Port + "|" + v.Item.PID + "|" + v.Item.Proto
}

func guardPath() string {
	home, _ := os.UserHomeDir()
	if su := os.Getenv("SUDO_USER"); su != "" {
		if u, err := user.Lookup(su); err == nil && u.HomeDir != "" {
			home = u.HomeDir
		}
	}
	return filepath.Join(home, ".config", "portkiller", "guard.conf")
}

func loadGuard() []Rule {
	b, err := os.ReadFile(guardPath())
	if err != nil {
		return nil
	}
	return parseGuard(string(b))
}

func violationText(vs []Violation) string {
	if len(vs) == 0 {
		return ""
	}
	v := vs[0]
	s := " GUARD: :" + v.Rule.Port + " owned by " + v.Rule.Owner + ", used by " + v.Item.Process + " (PID " + v.Item.PID + ")"
	if len(vs) > 1 {
		s += " +" + itoaSimple(len(vs)-1) + " more"
	}
	return s
}

func itoaSimple(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
