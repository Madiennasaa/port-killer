package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// hiddenPath holds process names the user hid from the table, one per line.
func hiddenPath() string {
	return filepath.Join(homeDir(), ".config", "portkiller", "hidden")
}

// parseHidden (pure): one name per line; "#" comments and blanks are skipped.
func parseHidden(data string) map[string]bool {
	out := map[string]bool{}
	for _, l := range strings.Split(data, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			out[l] = true
		}
	}
	return out
}

func loadHidden() map[string]bool {
	b, err := os.ReadFile(hiddenPath())
	if err != nil {
		return map[string]bool{}
	}
	return parseHidden(string(b))
}

// saveHidden writes the hidden set sorted, so the file diffs cleanly, and
// hands it to the sudo user like the other state files.
func saveHidden(path string, hidden map[string]bool) error {
	names := make([]string, 0, len(hidden))
	for n := range hidden {
		names = append(names, n)
	}
	sort.Strings(names)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data := "# process names hidden by portkiller (H toggles, U shows them)\n" + strings.Join(names, "\n") + "\n"
	if err := writeFileNoFollow(path, []byte(data), 0o644); err != nil {
		return err
	}
	chownToSudoUser(filepath.Dir(path), path)
	return nil
}
