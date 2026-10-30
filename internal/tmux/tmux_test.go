package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSplitFields(t *testing.T) {
	tests := []struct {
		line string
		want []string
	}{
		{`a\037b\037c`, []string{"a", "b", "c"}},
		{"a\x1fb", []string{"a", "b"}},
		{`a\\b\037c`, []string{`a\b`, "c"}},
		{`a\\037`, []string{`a\037`}}, // an escaped backslash, then plain text
		{`x\001y`, []string{"x\x01y"}},
		{"t\tab", []string{"t\tab"}},
		{`end\037`, []string{"end", ""}},
		{`\09`, []string{`\09`}},
	}
	for _, tt := range tests {
		if got := splitFields(tt.line); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitFields(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestParsePanes(t *testing.T) {
	line := func(f ...string) string { return strings.Join(f, `\037`) }
	geometry := line("0", "0", "80", "24", "1", "2", "80", "24", "0", "*Z", "0")
	out := strings.Join([]string{
		line("$1", "api", "1", "@2", "2", "server", "0", "%5", "0", "1", "go", "/srv", "host", geometry),
		line("$1", "api", "1", "@1", "1", "editor", "1", "%3", "1", "1", "nvim", "/srv", "host", geometry),
		line("$1", "api", "1", "@1", "1", "editor", "1", "%2", "0", "0", "zsh", "/srv", "host", geometry),
		line("$2", `a\\b c`, "0", "@1", "0", "editor", "1", "%2", "0", "1", "zsh", "/", "host", geometry),
	}, "\n") + "\n"
	sessions, err := ParsePanes(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || sessions[0].Name != "api" || sessions[1].Name != `a\b c` {
		t.Fatalf("sessions = %+v", sessions)
	}
	api := sessions[0]
	if !api.Attached || len(api.Windows) != 2 || api.Windows[0].Name != "editor" || api.Windows[1].Name != "server" {
		t.Fatalf("api windows not sorted by index: %+v", api.Windows)
	}
	editor := api.Windows[0]
	if editor.Panes[0].ID != "%2" || editor.Panes[1].ID != "%3" || editor.ActivePane().ID != "%3" {
		t.Errorf("editor panes = %+v", editor.Panes)
	}
	if editor.Flags != "*Z" {
		t.Errorf("flags = %q", editor.Flags)
	}
	if api.ActiveWindow() != editor {
		t.Errorf("active window = %+v", api.ActiveWindow())
	}
	// A window linked into two sessions belongs to each separately.
	if w := sessions[1].Windows; len(w) != 1 || len(w[0].Panes) != 1 {
		t.Errorf("linked window = %+v", w)
	}
	if _, err := ParsePanes("only\\037two\n"); err == nil {
		t.Error("want an error for a short line")
	}
}

// server starts an isolated tmux server for one test.
func server(t *testing.T) Tmux {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	tm := Tmux{Args: []string{"-S", filepath.Join(t.TempDir(), "sock"), "-f", "/dev/null"}}
	t.Cleanup(func() { tm.Run("kill-server") })
	return tm
}

func mustRun(t *testing.T, tm Tmux, args ...string) string {
	t.Helper()
	out, err := tm.Run(args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func TestSessions(t *testing.T) {
	tm := server(t)
	api := mustRun(t, tm, "new-session", "-d", "-P", "-F", "#{session_id}", "-s", "api", "-n", "editor", "-x", "80", "-y", "24")
	mustRun(t, tm, "split-window", "-t", api)
	mustRun(t, tm, "new-window", "-d", "-t", api, "-n", "w:1.x")
	mustRun(t, tm, "new-session", "-d", "-s", `wïn \ dow`)

	sessions, err := tm.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range sessions {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"api", `wïn \ dow`}) {
		t.Fatalf("session names = %q", names)
	}
	w := sessions[0].Windows
	if len(w) != 2 || w[0].Name != "editor" || len(w[0].Panes) != 2 || w[1].Name != "w:1.x" {
		t.Errorf("api windows = %+v", w)
	}
}

func TestSwitch(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("attaching a test client needs util-linux script")
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script not installed")
	}
	tm := server(t)
	mustRun(t, tm, "new-session", "-d", "-s", "a")
	b := mustRun(t, tm, "new-session", "-d", "-P", "-F", "#{session_id}", "-s", "b")
	win := mustRun(t, tm, "new-window", "-d", "-P", "-F", "#{window_id}", "-t", b)
	pane := mustRun(t, tm, "split-window", "-d", "-P", "-F", "#{pane_id}", "-t", win)

	client := attach(t, tm, "a")
	if err := tm.Switch(client, Target{Session: b, Window: win, Pane: pane}); err != nil {
		t.Fatal(err)
	}
	loc, err := tm.Current(client)
	if err != nil {
		t.Fatal(err)
	}
	if loc.Session != b || loc.Window != win || loc.Pane != pane {
		t.Errorf("client at %+v, want %s %s %s", loc, b, win, pane)
	}
	if _, err := tm.Current("/dev/nonexistent"); err == nil {
		t.Error("want an error for an unknown client")
	}
	if err := tm.Switch(client, Target{Session: "$999"}); err == nil {
		t.Error("want an error for a missing session")
	}
}

// attach attaches a client to session through a pseudo-terminal and returns
// its name.
func attach(t *testing.T, tm Tmux, session string) string {
	t.Helper()
	cmd := exec.Command("script", "-qfc", "tmux "+strings.Join(tm.Args, " ")+" attach -t "+session, "/dev/null")
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "TMUX=") && !strings.HasPrefix(e, "TMUX_PANE=") && !strings.HasPrefix(e, "TERM=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	for i := 0; i < 50; i++ {
		if out, _ := tm.Run("list-clients", "-F", "#{client_name}"); strings.TrimSpace(out) != "" {
			return strings.TrimSpace(out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("test client did not attach")
	return ""
}

func TestCapture(t *testing.T) {
	tm := server(t)
	p0 := mustRun(t, tm, "new-session", "-d", "-P", "-F", "#{pane_id}", "-x", "80", "-y", "24",
		"printf '\\033[31mred\\033[0m\\n'; sleep 30")
	p1 := mustRun(t, tm, "split-window", "-d", "-P", "-F", "#{pane_id}", "echo plain; sleep 30")
	time.Sleep(300 * time.Millisecond)
	screens, err := tm.Capture(p0, p1)
	if err != nil {
		t.Fatal(err)
	}
	if s := screens[p0]; !strings.Contains(s.Text, "\x1b[31mred") || strings.Contains(s.Text, "plain") || s.CursorY != 1 {
		t.Errorf("pane %s = %+v", p0, s)
	}
	if s := screens[p1]; !strings.HasPrefix(s.Text, "plain\n") || s.CursorY != 1 || s.CursorX != 0 {
		t.Errorf("pane %s = %+v", p1, s)
	}
	sessions, err := tm.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	w := sessions[0].Windows[0]
	if w.Width != 80 || w.Height != 24 || w.Panes[0].Height+w.Panes[1].Height != 23 {
		t.Errorf("geometry: window %dx%d, panes %+v %+v", w.Width, w.Height, *w.Panes[0], *w.Panes[1])
	}
}
