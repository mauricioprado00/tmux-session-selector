// Package tmuxconf adds and removes the selector's key binding, both in a
// tmux configuration file and in the running tmux server.
package tmuxconf

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

// The binding is kept between these lines, so it can be replaced or removed
// without touching the rest of the file.
const (
	begin          = "# >>> tmux-session-selector >>>"
	end            = "# <<< tmux-session-selector <<<"
	keyPrefix      = "# key: "
	previousPrefix = "# previous: "
)

// DefaultPath is the configuration file tmux reads: the first that exists
// of ~/.tmux.conf, $XDG_CONFIG_HOME/tmux/tmux.conf and
// ~/.config/tmux/tmux.conf, or else ~/.tmux.conf.
func DefaultPath(home, xdgConfigHome string) string {
	candidates := []string{filepath.Join(home, ".tmux.conf")}
	if xdgConfigHome != "" {
		candidates = append(candidates, filepath.Join(xdgConfigHome, "tmux", "tmux.conf"))
	}
	candidates = append(candidates, filepath.Join(home, ".config", "tmux", "tmux.conf"))
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[0]
}

// Binding is the selector's key binding in tmux's prefix table.
type Binding struct {
	Key   string   // e.g. "w"
	Argv  []string // the selector: its absolute path, then any arguments
	Popup bool     // open in display-popup (tmux 3.2+), else in a new window
	// Previous is what the key did before, as list-keys prints it, so that
	// removing the binding can restore it in the running server.
	Previous string
}

// Args is the binding as tmux command arguments.
func (b Binding) Args() []string {
	cmd := shellJoin(b.Argv)
	if b.Popup {
		return []string{"bind-key", "-T", "prefix", b.Key, "display-popup", "-E", "-w", "80%", "-h", "80%", cmd}
	}
	return []string{"bind-key", "-T", "prefix", b.Key, "new-window", "-n", "select", cmd}
}

// Line is the binding as a configuration file line.
func (b Binding) Line() string {
	var words []string
	for _, a := range b.Args() {
		words = append(words, tmuxQuote(a))
	}
	return strings.Join(words, " ")
}

func (b Binding) block() string {
	lines := []string{begin,
		"# Added by tmux-session-selector -tmux-bind; remove it with -tmux-unbind.",
		keyPrefix + b.Key}
	if b.Previous != "" {
		lines = append(lines, previousPrefix+b.Previous)
	}
	return strings.Join(append(lines, b.Line(), end), "\n") + "\n"
}

// Add returns content with b's block in place of an existing one, or else
// appended after a blank line, which Remove takes away again.
func Add(content string, b Binding) string {
	rest, _ := Remove(content)
	if rest != "" && !strings.HasSuffix(rest, "\n") {
		rest += "\n"
	}
	if rest != "" {
		rest += "\n"
	}
	return rest + b.block()
}

// Remove returns content without the selector's block, and the binding the
// block held (only its Key and Previous are set), or nil if there is none.
// The blank line Add puts before the block goes with it.
func Remove(content string) (string, *Binding) {
	lines := strings.SplitAfter(content, "\n")
	start, stop := -1, -1
	for i, l := range lines {
		switch strings.TrimRight(l, "\r\n") {
		case begin:
			if start < 0 {
				start = i
			}
		case end:
			if start >= 0 && stop < 0 {
				stop = i
			}
		}
	}
	if start < 0 || stop < 0 {
		return content, nil
	}
	b := &Binding{}
	for _, l := range lines[start:stop] {
		l = strings.TrimRight(l, "\r\n")
		if v, ok := strings.CutPrefix(l, keyPrefix); ok {
			b.Key = v
		}
		if v, ok := strings.CutPrefix(l, previousPrefix); ok {
			b.Previous = v
		}
	}
	if start > 0 && strings.TrimSpace(lines[start-1]) == "" {
		start--
	}
	return strings.Join(append(lines[:start:start], lines[stop+1:]...), ""), b
}

// Install writes b into the configuration file at path, creating the file if
// need be, and binds it in the running tmux server if there is one, which
// live reports. Previous is filled in from the server, or kept from an
// earlier install on the same key, so that Uninstall restores the original
// binding and not a selector one.
func Install(tm tmux.Tmux, path string, b Binding) (live bool, err error) {
	content, mode, err := read(path)
	if err != nil {
		return false, err
	}
	_, old := Remove(content)
	live = serverRunning(tm)
	switch {
	case old != nil && old.Key == b.Key:
		b.Previous = old.Previous
	case live:
		b.Previous = currentBinding(tm, b.Key)
	}
	if err := write(path, Add(content, b), mode); err != nil {
		return false, err
	}
	if !live {
		return false, nil
	}
	if old != nil && old.Key != b.Key {
		restore(tm, *old)
	}
	_, err = tm.Run(b.Args()...)
	return err == nil, err
}

// Uninstall removes the selector's block from the configuration file at
// path and, in the running tmux server if there is one, unbinds its key and
// restores what the key did before. It returns the binding removed, or nil
// if the file had none.
func Uninstall(tm tmux.Tmux, path string) (removed *Binding, live bool, err error) {
	content, mode, err := read(path)
	if err != nil {
		return nil, false, err
	}
	rest, old := Remove(content)
	if old == nil {
		return nil, false, nil
	}
	if err := write(path, rest, mode); err != nil {
		return nil, false, err
	}
	if !serverRunning(tm) {
		return old, false, nil
	}
	return old, true, restore(tm, *old)
}

// restore unbinds b's key in the server and binds what it did before.
func restore(tm tmux.Tmux, b Binding) error {
	tm.Run("unbind-key", "-T", "prefix", b.Key) // it may have been unbound by hand
	if b.Previous == "" {
		return nil
	}
	return tm.RunLine(b.Previous)
}

func serverRunning(tm tmux.Tmux) bool {
	_, err := tm.Run("list-sessions")
	return err == nil
}

// currentBinding is the key's binding in the server, as list-keys prints
// it, or "" if it has none.
func currentBinding(tm tmux.Tmux, key string) string {
	out, err := tm.Run("list-keys", "-T", "prefix", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// HasPopup reports whether tmux's version, as tmux -V prints it (e.g.
// "tmux 3.4" or "tmux next-3.5"), has display-popup, which came in 3.2.
func HasPopup(version string) bool {
	m := regexp.MustCompile(`(\d+)\.(\d+)`).FindStringSubmatch(version)
	if m == nil {
		return true // unknown, such as a development build: assume a recent one
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 3 || (major == 3 && minor >= 2)
}

func read(path string) (content string, mode fs.FileMode, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", 0o644, nil
	}
	if err != nil {
		return "", 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	return string(data), info.Mode().Perm(), nil
}

// write writes the file in place, following a symlink rather than replacing
// it, as configuration files are often links into a dotfiles repository.
func write(path, content string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), mode)
}

// tmuxQuote quotes a word for a tmux configuration file. Inside double
// quotes tmux expands $VARIABLES and backslash escapes, so those are escaped.
func tmuxQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./%:=+@^,") == "" {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`)
	return `"` + r.Replace(s) + `"`
}

// shellJoin joins words into a shell command, quoting those that need it.
func shellJoin(words []string) string {
	var out []string
	for _, w := range words {
		if w != "" && strings.Trim(w, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./%:=+@,") == "" {
			out = append(out, w)
		} else {
			out = append(out, "'"+strings.ReplaceAll(w, "'", `'\''`)+"'")
		}
	}
	return strings.Join(out, " ")
}
