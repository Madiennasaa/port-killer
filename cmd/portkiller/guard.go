package main

import (
	"os"
	"path/filepath"
	"strings"
)

// Rule: a port is expected to be used by a specific owner (matches Process or Project).
type Rule struct {
	Port  string
	Owner string
}

// parseGuard (pure): lines like "5432=postgres-dev"; "#" comments and blanks are skipped.
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

// ownerMatches (pure): owner matches the process name (without "docker:") or project.
func ownerMatches(it PortItem, owner string) bool {
	name := strings.TrimPrefix(it.Process, "docker:")
	o := strings.ToLower(owner)
	return strings.ToLower(name) == o || strings.ToLower(it.Project) == o
}

// Violation: a port taken by a process that is not its owner.
type Violation struct {
	Rule Rule
	Item PortItem
}

// checkGuard (pure): TCP/UDP listeners on a rule's port that are not the owner.
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

// guardKey dedups violations so history is not flooded every tick.
func guardKey(v Violation) string {
	return v.Rule.Port + "|" + v.Item.PID + "|" + v.Item.Proto
}

// homeDir returns the effective home directory: $SUDO_USER's home when running
// under sudo, else $HOME. On lookup failure it returns "" so callers can fall
// back rather than silently reading /root or a bogus path.
func homeDir() string {
	su := os.Getenv("SUDO_USER")
	if su != "" {
		if h, ok := lookupHomeForUid("", su, homeByUID, homeByName); ok {
			return h
		}
	}
	home, err := userHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func guardPath() string {
	return filepath.Join(homeDir(), ".config", "portkiller", "guard.conf")
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
