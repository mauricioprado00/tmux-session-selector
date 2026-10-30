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

func typeText(m *model, s string) {
	for _, r := range s {
		m.handle(typed(r))
	}
}

func windowNames(t *testing.T, tm tmux.Tmux, session string) string {
	return strings.ReplaceAll(tmuxRun(t, tm, "list-windows", "-t", session, "-F", "#{window_name}"), "\n", " ")
}

func TestTags(t *testing.T) {
	tm := tmuxServer(t)
	tmuxRun(t, tm, "new-session", "-d", "-s", "a", "-n", "w0")
	for _, name := range []string{"w1", "w2", "w3"} {
		tmuxRun(t, tm, "new-window", "-d", "-t", "a", "-n", name)
	}
	m := liveModel(t, tm)

	// Alt-t tags and moves down, so two presses tag w1 and w2.
	m.selectRow(t, "w1")
	m.handle(alt('t'))
	m.handle(alt('t'))
	if len(m.tags) != 2 || m.selected().name != "w3" {
		t.Fatalf("tags %v, selected %q", m.tags, m.selected().name)
	}
	m.handle(alt(':'))
	if m.input == nil || m.input.label != "command for 2 tagged items: " {
		t.Fatalf("input = %+v", m.input)
	}
	typeText(m, "rename-window -t '%%' tagged")
	m.handle(event{key: keyEnter})
	if got := windowNames(t, tm, "a"); got != "w0 tagged tagged w3" {
		t.Errorf("after the command: %q", got)
	}
	if len(m.tags) != 2 {
		t.Errorf("tags should survive the reload: %v", m.tags)
	}

	m.handle(alt('X'))
	if m.confirm == nil || m.confirm.question != "Kill 2 tagged items?" {
		t.Fatalf("confirm = %+v", m.confirm)
	}
	m.handle(typed('y'))
	if got := windowNames(t, tm, "a"); got != "w0 w3" || len(m.tags) != 0 {
		t.Errorf("after killing tagged: %q, tags %v", got, m.tags)
	}

	// Nothing tagged: Alt-: acts on the selected row.
	m.selectRow(t, "w3")
	m.handle(alt(';'))
	typeText(m, "rename-window -t '%%' solo")
	m.handle(event{key: keyEnter})
	if got := windowNames(t, tm, "a"); got != "w0 solo" {
		t.Errorf("command on the selected row: %q", got)
	}

	// Ctrl-T tags every match; Alt-T clears.
	typeText(m, "w0")
	m.handle(event{key: keyTagAll})
	if len(m.tags) != 1 {
		t.Errorf("Ctrl-T should tag the one match, got %v", m.tags)
	}
	m.handle(alt('T'))
	if len(m.tags) != 0 {
		t.Errorf("Alt-T should clear tags, got %v", m.tags)
	}
	m.handle(alt(':'))
	m.handle(event{key: keyCancel})
	if m.input != nil || len(m.query) != 2 {
		t.Errorf("Esc should close the prompt and keep the query")
	}
}

func TestMark(t *testing.T) {
	tm := tmuxServer(t)
	tmuxRun(t, tm, "new-session", "-d", "-s", "a", "-n", "one")
	tmuxRun(t, tm, "new-window", "-d", "-t", "a", "-n", "two")
	m := liveModel(t, tm)
	marked := func() string {
		return tmuxRun(t, tm, "list-panes", "-a", "-F", "#{?pane_marked,#{window_name},}")
	}
	m.selectRow(t, "two")
	m.handle(alt('m'))
	if got := marked(); got != "two" {
		t.Fatalf("marked pane is in %q, want two", got)
	}
	if !strings.Contains(m.rowText(m.selected()), "two-M") && !strings.Contains(m.rowText(m.selected()), "twoM") {
		t.Errorf("row should show the M flag: %q", m.rowText(m.selected()))
	}
	m.handle(alt('M'))
	if got := marked(); got != "" {
		t.Errorf("Alt-M should clear the mark, still in %q", got)
	}
}

func TestSessionGroups(t *testing.T) {
	tm := tmuxServer(t)
	tmuxRun(t, tm, "new-session", "-d", "-s", "a")
	tmuxRun(t, tm, "new-session", "-d", "-t", "a", "-s", "a2")
	tmuxRun(t, tm, "new-session", "-d", "-s", "solo")
	sessionNames := func(m *model) string {
		var s []string
		for _, n := range m.roots {
			s = append(s, n.name)
		}
		return strings.Join(s, " ")
	}
	m := liveModel(t, tm)
	if got := sessionNames(m); got != "a solo" {
		t.Errorf("one session per group: %q", got)
	}
	if got := m.rowText(m.roots[0]); !strings.Contains(got, "group a: a,a2") {
		t.Errorf("session row = %q", got)
	}
	m = &model{tmux: tm, allGroups: true}
	m.load()
	if got := sessionNames(m); got != "a a2 solo" {
		t.Errorf("with all groups: %q", got)
	}
}

func TestSquashGroupsKeepsCurrent(t *testing.T) {
	sessions := []*tmux.Session{
		{ID: "$3", Name: "b", Group: "g"},
		{ID: "$1", Name: "c", Group: "g"},
		{ID: "$2", Name: "d"},
	}
	name := func(ss []*tmux.Session) string {
		var s []string
		for _, x := range ss {
			s = append(s, x.Name)
		}
		return strings.Join(s, " ")
	}
	if got := name(squashGroups(sessions, "")); got != "c d" {
		t.Errorf("first of group: %q", got)
	}
	if got := name(squashGroups(sessions, "$3")); got != "b d" {
		t.Errorf("client's own session: %q", got)
	}
}

func TestOverviewFollowsWindows(t *testing.T) {
	tm := tmuxServer(t)
	tmuxRun(t, tm, "new-session", "-d", "-s", "a", "-n", "w0", "-x", "80", "-y", "24", "echo zero; sleep 30")
	w1 := tmuxRun(t, tm, "new-window", "-d", "-P", "-F", "#{window_id}", "-t", "a", "-n", "w1", "echo one; sleep 30")
	tmuxRun(t, tm, "new-window", "-d", "-t", "a", "-n", "w2", "echo two; sleep 30")
	time.Sleep(200 * time.Millisecond)
	m := liveModel(t, tm)
	m.selectRow(t, "w1")
	m.handle(event{key: keyOverview}) // centred on w1
	m.render(120, 40)

	// A window closes behind the overview's back: the others keep their content.
	tmuxRun(t, tm, "kill-window", "-t", "a:w2")
	m.screens = map[string]screen{}
	m.render(120, 40)
	if got := len(m.screens[m.overview.session.Windows[0].Panes[0].ID].cells); got == 0 {
		t.Error("w0 lost its content because another window closed")
	}

	// On refresh the grid follows: w2 goes, a new window comes, focus and
	// centre stay on w1.
	tmuxRun(t, tm, "new-window", "-d", "-t", "a", "-n", "w3")
	m.refreshOverview()
	var windowNames []string
	for _, w := range m.overview.session.Windows {
		windowNames = append(windowNames, w.Name)
	}
	if got := strings.Join(windowNames, " "); got != "w0 w1 w3" {
		t.Errorf("windows = %q", got)
	}
	if ov := m.overview; ov.center == nil || ov.center.ID != w1 || ov.session.Windows[ov.focus].ID != w1 {
		t.Errorf("focus and centre should stay on w1: %+v", ov)
	}

	// The centre window closes: the windows share the screen evenly.
	tmuxRun(t, tm, "kill-window", "-t", w1)
	m.refreshOverview()
	if m.overview.center != nil || len(m.overview.session.Windows) != 2 {
		t.Errorf("after the centre closed: %+v", m.overview)
	}

	// Back in the list, it is read again.
	m.handle(typed('q'))
	if got := strings.Join(names(m.rows), " "); got != "a w0 w3" {
		t.Errorf("list after the overview = %q", got)
	}

	// The whole session goes: the overview closes with a note.
	m.selectRow(t, "a")
	m.handle(event{key: keyOverview})
	tmuxRun(t, tm, "new-session", "-d", "-s", "b") // keep the server alive
	tmuxRun(t, tm, "kill-session", "-t", "a")
	m.refreshOverview()
	if m.overview != nil || m.status != "the session has gone" {
		t.Errorf("overview %v, status %q", m.overview, m.status)
	}
}
