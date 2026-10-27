package main

import (
	"sort"
	"strconv"
	"strings"
)

// Group is one project: its items (port rows) and unique kill targets.
type Group struct {
	Project string
	Items   []PortItem
}

// groupByProject (pure): groups by Project, skipping "-" and "".
// Urut nama proyek.
func groupByProject(items []PortItem) []Group {
	idx := map[string]int{}
	var gs []Group
	for _, it := range items {
		if it.Project == "" || it.Project == "-" {
			continue
		}
		i, ok := idx[it.Project]
		if !ok {
			i = len(gs)
			idx[it.Project] = i
			gs = append(gs, Group{Project: it.Project})
		}
		gs[i].Items = append(gs[i].Items, it)
	}
	sort.Slice(gs, func(a, b int) bool { return gs[a].Project < gs[b].Project })
	return gs
}

// projectTargets (pure): unique kill targets per PID/container.
// Skips systemProcs and root/service uids (0..999). uidOf is injected for testability.
func projectTargets(g Group, uidOf func(pid int) (int, bool)) []PortItem {
	seen := map[string]bool{}
	var out []PortItem
	for _, it := range g.Items {
		if seen[it.PID] {
			continue
		}
		if !it.IsDocker() {
			if systemProcs[it.Process] {
				continue
			}
			if n, err := strconv.Atoi(it.PID); err == nil {
				if uid, ok := uidOf(n); ok && uid <= 999 {
					continue
				}
			}
		}
		seen[it.PID] = true
		out = append(out, it)
	}
	return out
}

// projectSummary (pure): counts of host processes and containers.
func projectSummary(targets []PortItem) (host, docker int) {
	for _, t := range targets {
		if t.IsDocker() {
			docker++
		} else {
			host++
		}
	}
	return
}

func projectNames(targets []PortItem, max int) string {
	var n []string
	for _, t := range targets {
		n = append(n, t.Process+"(:"+t.Port+")")
	}
	if len(n) > max {
		return strings.Join(n[:max], ", ") + " +" + strconv.Itoa(len(n)-max) + " more"
	}
	return strings.Join(n, ", ")
}
