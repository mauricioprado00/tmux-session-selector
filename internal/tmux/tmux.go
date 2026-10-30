// Package tmux runs the tmux binary and turns its output into a tree of
// sessions, windows and panes.
package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// sep separates fields in format strings. tmux prints it as the octal escape
// \037, which splitFields decodes.
const sep = '\x1f'

type Pane struct {
	ID      string // %7
	Index   int
	Active  bool
	Command string
	Path    string
	Title   string
	Marked  bool // the marked pane (select-pane -m)
	// Position and size within the window, and cursor position within the pane.
	Left, Top, Width, Height int
	CursorX, CursorY         int
}

type Window struct {
	ID            string // @3
	Index         int
	Name          string
	Active        bool
	Width, Height int
	Zoomed        bool
	// Flags as tmux shows them after the name: * current, - last,
	// # activity, ! bell, ~ silence, M marked pane, Z zoomed.
	Flags    string
	Activity int64 // Unix time of the last activity
	Panes    []*Pane
}

// ActivePane returns the window's active pane, or its first one.
func (w *Window) ActivePane() *Pane {
	for _, p := range w.Panes {
		if p.Active {
			return p
		}
	}
	if len(w.Panes) > 0 {
		return w.Panes[0]
	}
	return nil
}

type Session struct {
	ID       string // $1
	Name     string
	Attached bool
	Activity int64 // Unix time of the last activity
	// Group is the session group's name and GroupList its members, as
	// "a,b"; both are empty for a session not in a group.
	Group, GroupList string
	Windows          []*Window
}

// ActiveWindow returns the session's current window, or its first one.
func (s *Session) ActiveWindow() *Window {
	for _, w := range s.Windows {
		if w.Active {
			return w
		}
	}
	if len(s.Windows) > 0 {
		return s.Windows[0]
	}
	return nil
}

// Location is where a client currently is.
type Location struct {
	Client, Session, Window, Pane string
}

// Target is where to jump to. Window and Pane may be empty.
type Target struct {
	Session, Window, Pane string
}

// String is the target in tmux's target syntax, using ids: $1, $1:@3 or
// $1:@3.%7.
func (t Target) String() string {
	s := t.Session
	if t.Window != "" {
		s += ":" + t.Window
	}
	if t.Pane != "" {
		s += "." + t.Pane
	}
	return s
}

// Tmux runs tmux commands. Args go before every command, for example
// {"-L", "socket"}.
type Tmux struct {
	Args []string
}

// Run runs one tmux command line and returns its standard output.
func (t Tmux) Run(args ...string) (string, error) {
	return t.run("", args...)
}

// RunTemplate runs a tmux command line written in tmux's own syntax, after
// replacing the first %% and every %1 with target, as choose-tree does.
func (t Tmux) RunTemplate(template string, target Target) error {
	line := strings.Replace(template, "%%", target.String(), 1)
	line = strings.ReplaceAll(line, "%1", target.String())
	_, err := t.run(line+"\n", "source-file", "-")
	return err
}

// run runs tmux with input on its standard input.
func (t Tmux) run(input string, args ...string) (string, error) {
	cmd := exec.Command("tmux", append(append([]string{}, t.Args...), args...)...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// source-file reports parse errors on standard output.
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New("tmux " + args[0] + ": " + msg)
	}
	return string(out), nil
}

var paneFields = []string{
	"session_id", "session_name", "session_attached",
	"window_id", "window_index", "window_name", "window_active",
	"pane_id", "pane_index", "pane_active",
	"pane_current_command", "pane_current_path", "pane_title",
	"pane_left", "pane_top", "pane_width", "pane_height", "cursor_x", "cursor_y",
	"window_width", "window_height", "window_zoomed_flag",
	"window_raw_flags", "pane_marked", "session_activity", "window_activity",
	"session_group", "session_group_list",
}

var locationFields = []string{"client_name", "session_id", "window_id", "pane_id"}

func format(fields []string) string {
	return "#{" + strings.Join(fields, "}"+string(sep)+"#{") + "}"
}

// Sessions lists every session, window and pane on the server.
func (t Tmux) Sessions() ([]*Session, error) {
	out, err := t.Run("list-panes", "-a", "-F", format(paneFields))
	if err != nil {
		return nil, err
	}
	return ParsePanes(out)
}

// ParsePanes builds the tree from list-panes output in the paneFields format.
// Sessions keep tmux's order; windows and panes are sorted by index.
func ParsePanes(out string) ([]*Session, error) {
	var sessions []*Session
	bySession := map[string]*Session{}
	byWindow := map[string]*Window{} // keyed by session and window: a window can be linked into several sessions
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		f := splitFields(line)
		if len(f) != len(paneFields) {
			return nil, fmt.Errorf("unexpected tmux output: %q", line)
		}
		s := bySession[f[0]]
		if s == nil {
			s = &Session{ID: f[0], Name: f[1], Attached: f[2] != "0", Activity: int64(atoi(f[24])),
				Group: f[26], GroupList: f[27]}
			bySession[f[0]] = s
			sessions = append(sessions, s)
		}
		w := byWindow[f[0]+f[3]]
		if w == nil {
			w = &Window{ID: f[3], Index: atoi(f[4]), Name: f[5], Active: f[6] == "1",
				Width: atoi(f[19]), Height: atoi(f[20]), Zoomed: f[21] == "1", Flags: f[22],
				Activity: int64(atoi(f[25]))}
			byWindow[f[0]+f[3]] = w
			s.Windows = append(s.Windows, w)
		}
		w.Panes = append(w.Panes, &Pane{
			ID: f[7], Index: atoi(f[8]), Active: f[9] == "1",
			Command: f[10], Path: f[11], Title: f[12],
			Left: atoi(f[13]), Top: atoi(f[14]), Width: atoi(f[15]), Height: atoi(f[16]),
			CursorX: atoi(f[17]), CursorY: atoi(f[18]), Marked: f[23] == "1",
		})
	}
	for _, s := range sessions {
		sort.SliceStable(s.Windows, func(i, j int) bool { return s.Windows[i].Index < s.Windows[j].Index })
		for _, w := range s.Windows {
			sort.SliceStable(w.Panes, func(i, j int) bool { return w.Panes[i].Index < w.Panes[j].Index })
		}
	}
	return sessions, nil
}

// Current returns where client is. An empty client means the calling one.
func (t Tmux) Current(client string) (Location, error) {
	if client == "" {
		out, err := t.Run("display-message", "-p", format(locationFields))
		if err != nil {
			return Location{}, err
		}
		return parseLocation(strings.TrimSuffix(out, "\n")), nil
	}
	out, err := t.Run("list-clients", "-F", format(locationFields))
	if err != nil {
		return Location{}, err
	}
	for _, line := range strings.Split(out, "\n") {
		if l := parseLocation(line); l.Client == client {
			return l, nil
		}
	}
	return Location{}, fmt.Errorf("tmux client %q not found", client)
}

func parseLocation(line string) Location {
	f := splitFields(line)
	for len(f) < len(locationFields) {
		f = append(f, "")
	}
	return Location{Client: f[0], Session: f[1], Window: f[2], Pane: f[3]}
}

// Switch moves client (the calling client when empty) to target, in one
// tmux invocation.
func (t Tmux) Switch(client string, target Target) error {
	args := selectArgs(target)
	if len(args) > 0 {
		args = append(args, ";")
	}
	args = append(args, "switch-client")
	if client != "" {
		args = append(args, "-c", client)
	}
	_, err := t.Run(append(args, "-t", target.Session)...)
	return err
}

// Attach replaces the current process with a tmux client attached to target.
// It returns only on error.
func (t Tmux) Attach(target Target) error {
	if args := selectArgs(target); len(args) > 0 {
		if _, err := t.Run(args...); err != nil {
			return err
		}
	}
	path, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	argv := append(append([]string{"tmux"}, t.Args...), "attach-session", "-t", target.Session)
	return syscall.Exec(path, argv, os.Environ())
}

func selectArgs(target Target) []string {
	var args []string
	if target.Window != "" {
		args = append(args, "select-window", "-t", target.Window)
	}
	if target.Pane != "" {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(args, "select-pane", "-t", target.Pane)
	}
	return args
}

// Screen is a pane's visible content, with colours and attributes as SGR
// escape sequences, and its cursor position at the time.
type Screen struct {
	Text             string
	CursorX, CursorY int
}

// Kill kills the target: its pane if it names one, else its window, else
// its session.
func (t Tmux) Kill(target Target) error {
	var err error
	switch {
	case target.Pane != "":
		_, err = t.Run("kill-pane", "-t", target.Pane)
	case target.Window != "":
		_, err = t.Run("kill-window", "-t", target.Window)
	default:
		_, err = t.Run("kill-session", "-t", target.Session)
	}
	return err
}

// Mark makes target's pane (the active one of a window or session) the
// marked pane, the default source of join-pane, swap-pane and move-pane.
func (t Tmux) Mark(target Target) error {
	_, err := t.Run("select-pane", "-m", "-t", target.String())
	return err
}

// ClearMark clears the marked pane.
func (t Tmux) ClearMark() error {
	_, err := t.Run("select-pane", "-M")
	return err
}

// Capture returns the screens of panes, keyed by pane id. All panes are
// captured in one tmux invocation; after each, display-message prints a
// marker line carrying the pane's cursor position.
func (t Tmux) Capture(ids ...string) (map[string]Screen, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	marker := fmt.Sprintf("tss-capture-end-%d-%d", os.Getpid(), time.Now().UnixNano())
	var args []string
	for _, id := range ids {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(args, "capture-pane", "-p", "-e", "-t", id,
			";", "display-message", "-p", "-t", id, marker+" #{cursor_x} #{cursor_y}")
	}
	out, err := t.Run(args...)
	if err != nil {
		return nil, err
	}
	screens := map[string]Screen{}
	var text strings.Builder
	i := 0
	for _, line := range strings.SplitAfter(out, "\n") {
		rest, ok := strings.CutPrefix(line, marker+" ")
		if !ok {
			text.WriteString(line)
			continue
		}
		if i < len(ids) {
			x, y, _ := strings.Cut(strings.TrimSpace(rest), " ")
			screens[ids[i]] = Screen{Text: text.String(), CursorX: atoi(x), CursorY: atoi(y)}
			i++
		}
		text.Reset()
	}
	return screens, nil
}

// splitFields splits a line on sep, decoding the escapes tmux writes for
// control characters (\ooo) and backslashes (\\). Older tmux versions print
// sep raw, which is handled too.
func splitFields(line string) []string {
	var fields []string
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && i+1 < len(line) && line[i+1] == '\\':
			b.WriteByte('\\')
			i++
			continue
		case c == '\\' && i+3 < len(line) && isOctal(line[i+1:i+4]):
			c = (line[i+1]-'0')<<6 | (line[i+2]-'0')<<3 | (line[i+3] - '0')
			i += 3
		}
		if c == sep {
			fields = append(fields, b.String())
			b.Reset()
		} else {
			b.WriteByte(c)
		}
	}
	return append(fields, b.String())
}

func isOctal(s string) bool {
	return s[0] >= '0' && s[0] <= '3' &&
		s[1] >= '0' && s[1] <= '7' &&
		s[2] >= '0' && s[2] <= '7'
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
