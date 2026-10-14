package main

import (
	"sort"
	"strconv"
	"strings"
)

// Group satu proyek: item (baris port) dan target kill unik.
type Group struct {
	Project string
	Items   []PortItem
}

// groupByProject murni: kelompokkan per Project, lewati "-" dan "".
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

// projectTargets murni: target kill unik per PID/container.
// Lewati systemProcs dan uid service (1..999). uidOf diinject agar testable.
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
				if uid, ok := uidOf(n); ok && uid >= 1 && uid <= 999 {
					continue
				}
			}
		}
		seen[it.PID] = true
		out = append(out, it)
	}
	return out
}

// projectSummary murni: ringkasan jumlah proses host dan container.
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
