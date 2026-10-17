package main

import (
	"os"
	"strconv"
	"strings"
)

// Need is a host port a project expects to use, and where it was declared.
type Need struct {
	Port   string
	Source string
}

func numeric(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil && s != ""
}

// parseComposePorts (pure): host ports from "- 8080:80", "- 127.0.0.1:5432:5432/tcp", etc.
func parseComposePorts(data string) []string {
	var out []string
	for _, l := range strings.Split(data, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "-") {
			continue
		}
		l = strings.Trim(strings.TrimSpace(strings.TrimPrefix(l, "-")), "\"'")
		if i := strings.Index(l, "#"); i >= 0 {
			l = strings.TrimSpace(l[:i])
		}
		l = strings.Trim(l, "\"'")
		if i := strings.Index(l, "/"); i >= 0 && !strings.HasPrefix(l, "/") && !strings.HasPrefix(l, ".") {
			l = l[:i]
		}
		parts := strings.Split(l, ":")
		if len(parts) < 2 {
			continue
		}
		if h := parts[len(parts)-2]; numeric(h) {
			out = append(out, h)
		}
	}
	return out
}

// parseEnvPorts (pure): numeric values of KEY=VALUE where KEY ends with PORT.
func parseEnvPorts(data string) []string {
	var out []string
	for _, l := range strings.Split(data, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		i := strings.Index(l, "=")
		if i <= 0 {
			continue
		}
		k := strings.ToUpper(strings.TrimSpace(l[:i]))
		v := strings.Trim(strings.TrimSpace(l[i+1:]), "\"'")
		if strings.HasSuffix(k, "PORT") && numeric(v) {
			out = append(out, v)
		}
	}
	return out
}

// conflictDir is the directory whose compose/.env files we check: the working
// directory, or "" when it cannot be determined (the report then lists
// nothing declared, which is honest).
func conflictDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func readNeeds(dir string) []Need {
	var out []Need
	for _, f := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if b, err := os.ReadFile(dir + "/" + f); err == nil {
			for _, p := range parseComposePorts(string(b)) {
				out = append(out, Need{p, f})
			}
		}
	}
	if b, err := os.ReadFile(dir + "/.env"); err == nil {
		for _, p := range parseEnvPorts(string(b)) {
			out = append(out, Need{p, ".env"})
		}
	}
	return out
}

// conflictLines (pure): report lines for needs already taken by running items.
func conflictLines(needs []Need, items []PortItem) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range needs {
		k := n.Port + "|" + n.Source
		if seen[k] {
			continue
		}
		seen[k] = true
		taken := ""
		for _, it := range items {
			if it.Port == n.Port {
				taken = it.Process + " (PID " + it.PID + ")"
				break
			}
		}
		if taken != "" {
			out = append(out, " CONFLICT  :"+n.Port+" ("+n.Source+") is used by "+taken)
		} else {
			out = append(out, " free      :"+n.Port+" ("+n.Source+")")
		}
	}
	if len(out) == 0 {
		out = append(out, " No ports declared in docker-compose.yml / compose.yml / .env of the current directory.")
	}
	return out
}
