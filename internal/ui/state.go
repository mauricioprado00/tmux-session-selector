package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Settings the selector remembers between runs live in a small file of
// "name=value" lines, so toggling one now sets it for next time.

// DefaultStateFile is where settings are remembered:
// $XDG_STATE_HOME/tmux-session-selector/state, or under ~/.local/state when
// XDG_STATE_HOME is not set. It is "" when there is no home directory.
func DefaultStateFile() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "tmux-session-selector", "state")
}

// readState reads the settings in path. A missing or unreadable file, or
// an empty path, gives no settings.
func readState(path string) map[string]string {
	state := map[string]string{}
	if path == "" {
		return state
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return state
	}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && k != "" {
			state[k] = v
		}
	}
	return state
}

// saveSetting sets one setting in path, keeping the others. It does nothing
// for an empty path, and returns the error for the caller to show.
func saveSetting(path, name, value string) error {
	return updateState(path, func(state map[string]string) { state[name] = value })
}

// updateState lets change edit the settings in path, then writes them back
// if they changed. It does nothing for an empty path.
func updateState(path string, change func(state map[string]string)) error {
	if path == "" {
		return nil
	}
	state := readState(path)
	before := fmt.Sprint(state) // maps print sorted by key
	change(state)
	if fmt.Sprint(state) == before {
		return nil
	}
	var b strings.Builder
	for _, k := range sortedKeys(state) {
		b.WriteString(k + "=" + state[k] + "\n")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Written whole and renamed into place, so a reader never sees half.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// onOff is how a boolean setting is written.
func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
