package ui

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

// tmuxServer starts an isolated tmux server for one test.
func tmuxServer(t *testing.T) tmux.Tmux {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	tm := tmux.Tmux{Args: []string{"-S", filepath.Join(t.TempDir(), "sock"), "-f", "/dev/null"}}
	t.Cleanup(func() { tm.Run("kill-server") })
	return tm
}

func tmuxRun(t *testing.T, tm tmux.Tmux, args ...string) string {
	t.Helper()
	out, err := tm.Run(args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// liveModel loads a model from tm's server, as if run outside tmux.
func liveModel(t *testing.T, tm tmux.Tmux) *model {
	t.Helper()
	m := &model{tmux: tm}
	if err := m.load(); err != nil {
		t.Fatal(err)
	}
	m.setQuery()
	return m
}

// selectRow selects the first visible row whose name, or pane id, is name.
func (m *model) selectRow(t *testing.T, name string) {
	t.Helper()
	for i, r := range m.rows {
		if r.n.name == name || (r.n.kind == paneRow && r.n.pane.ID == name) {
			m.sel = i
			return
		}
	}
	t.Fatalf("no row %q in %q", name, names(m.rows))
}

func alt(r rune) event   { return event{key: keyAlt, r: r} }
func typed(r rune) event { return event{key: keyRune, r: r} }

func TestKill(t *testing.T) {
	tm := tmuxServer(t)
	tmuxRun(t, tm, "new-session", "-d", "-s", "a", "-n", "one")
	tmuxRun(t, tm, "new-window", "-d", "-t", "a", "-n", "two")
	tmuxRun(t, tm, "new-session", "-d", "-s", "b")
	m := liveModel(t, tm)

	m.selectRow(t, "two")
	m.handle(alt('x'))
	if m.confirm == nil || m.confirm.question != "Kill window 1: two?" {
		t.Fatalf("confirm = %+v", m.confirm)
	}
	m.handle(typed('n'))
	if m.confirm != nil || len(m.rows) != 5 {
		t.Fatalf("n should cancel, rows %q", names(m.rows))
	}
	m.handle(alt('x'))
	m.handle(typed('y'))
	if got := strings.Join(names(m.rows), ","); len(m.rows) != 4 || strings.Contains(got, "two") {
		t.Errorf("after killing two, rows = %s", got)
	}
	if m.selected().kind != sessionRow || m.selected().name != "b" {
		t.Errorf("selection should stay at the same position, got %q", m.selected().name)
	}
	m.handle(alt('x'))
	m.handle(typed('Y'))
	if len(m.rows) != 2 || m.rows[0].n.name != "a" {
		t.Errorf("after killing session b, rows = %q", names(m.rows))
	}
}

// rowText is the text of a row as drawn, without styles.
func (m *model) rowText(n *node) string {
	var b strings.Builder
	for _, s := range m.rowContent(n) {
		b.WriteString(s.text)
	}
	return b.String()
}

func TestFlags(t *testing.T) {
	tm := tmuxServer(t)
	tmuxRun(t, tm, "new-session", "-d", "-s", "a", "-n", "one", "-x", "80", "-y", "24")
	pane := tmuxRun(t, tm, "split-window", "-P", "-F", "#{pane_id}", "-t", "a:one")
	tmuxRun(t, tm, "resize-pane", "-Z", "-t", pane)
	tmuxRun(t, tm, "select-pane", "-m", "-t", pane)
	tmuxRun(t, tm, "new-window", "-d", "-t", "a", "-n", "two", "printf '\\a'; sleep 30")
	time.Sleep(300 * time.Millisecond)
	m := liveModel(t, tm)

	one, two := m.rows[1].n, m.rows[2].n
	if got := m.rowText(one); !strings.Contains(got, "one*MZ") || !strings.Contains(got, "(2 panes)") {
		t.Errorf("window one = %q", got)
	}
	if got := m.rowText(two); !strings.Contains(got, "two!") {
		t.Errorf("window two = %q", got)
	}
	one.expanded = true
	m.refresh()
	m.selectRow(t, pane)
	if got := m.rowText(m.selected()); !strings.Contains(got, pane+"M") {
		t.Errorf("marked pane row = %q", got)
	}
}

func TestCommandTemplate(t *testing.T) {
	tm := tmuxServer(t)
	tmuxRun(t, tm, "new-session", "-d", "-s", "a", "-n", "one")
	m := liveModel(t, tm)
	m.template = "rename-window -t '%%' chosen"
	m.selectRow(t, "one")
	if done, target, err := m.handle(event{key: keyEnter}); !done || target != nil || err != nil {
		t.Fatalf("Enter = %v %+v %v", done, target, err)
	}
	if got := tmuxRun(t, tm, "list-windows", "-t", "a", "-F", "#{window_name}"); got != "chosen" {
		t.Errorf("window name = %q", got)
	}
	m.template = "bogus-command"
	if done, _, _ := m.handle(event{key: keyEnter}); done || !strings.Contains(m.status, "bogus-command") {
		t.Errorf("a failing command should stay open with the error, got done=%v status %q", done, m.status)
	}
}
