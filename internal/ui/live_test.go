package ui

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
