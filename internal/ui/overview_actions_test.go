package ui

import (
	"strings"
	"testing"
)

func TestOverviewCommands(t *testing.T) {
	tm := tmuxServer(t)
	tmuxRun(t, tm, "new-session", "-d", "-s", "a", "-n", "w0")
	for _, name := range []string{"w1", "w2", "w3"} {
		tmuxRun(t, tm, "new-window", "-d", "-t", "a", "-n", name)
	}
	m := liveModel(t, tm)
	m.selectRow(t, "a")
	m.handle(event{key: keyOverview}) // every window, focus on w0
	ov := m.overview
	focused := func() string { return ov.session.Windows[ov.focus].Name }

	// x asks first; anything but y keeps the window.
	m.handle(typed('x'))
	if m.confirm == nil || m.confirm.question != "Kill window 0: w0?" {
		t.Fatalf("confirm = %+v", m.confirm)
	}
	if !strings.Contains(m.render(160, 40), "Kill window 0: w0? (y/n)") {
		t.Error("the question should be on the overview's top line")
	}
	m.handle(typed('n'))
	if got := windowNames(t, tm, "a"); got != "w0 w1 w2 w3" || m.overview == nil {
		t.Fatalf("after n: %q, overview open %v", got, m.overview != nil)
	}
	m.handle(typed('x'))
	m.handle(typed('y'))
	if got := windowNames(t, tm, "a"); got != "w1 w2 w3" || m.overview == nil || len(ov.session.Windows) != 3 {
		t.Fatalf("after x y: %q, overview windows %d", got, len(ov.session.Windows))
	}

	// t tags the window in focus and moves on, as in the list; the tags
	// are the list's.
	ov.focus = 0
	m.handle(typed('t'))
	m.handle(typed('t'))
	if len(m.tags) != 2 || focused() != "w3" {
		t.Fatalf("tags %v, focus on %s", m.tags, focused())
	}
	if screen := m.render(160, 40); !strings.Contains(screen, "✓ 1: w1") || !strings.Contains(screen, "2 tagged") {
		t.Error("tagged windows should be marked, and counted on the top line")
	}
	m.handle(typed(':'))
	if m.input == nil || m.input.label != "command for 2 tagged items: " {
		t.Fatalf("input = %+v", m.input)
	}
	typeText(m, "rename-window -t '%%' tagged")
	if screen := m.render(160, 40); !strings.Contains(screen, "command for 2 tagged items: ") ||
		!strings.Contains(screen, "rename-window -t '%%' tagged") {
		t.Error("the command should be typed on the overview's top line")
	}
	m.handle(event{key: keyEnter})
	if got := windowNames(t, tm, "a"); got != "tagged tagged w3" || m.overview == nil {
		t.Errorf("after the command: %q", got)
	}
	if ov.session.Windows[0].Name != "tagged" {
		t.Errorf("the overview should show the new names: %q", ov.session.Windows[0].Name)
	}

	// X kills the tagged windows, after asking.
	m.handle(typed('X'))
	if m.confirm == nil || m.confirm.question != "Kill 2 tagged items?" {
		t.Fatalf("confirm = %+v", m.confirm)
	}
	m.handle(typed('y'))
	if got := windowNames(t, tm, "a"); got != "w3" || len(m.tags) != 0 || len(ov.session.Windows) != 1 {
		t.Errorf("after X y: %q, tags %v", got, m.tags)
	}

	// Esc drops a command; T clears the tags.
	m.handle(typed('t'))
	m.handle(typed('T'))
	m.handle(typed(':'))
	m.handle(event{key: keyCancel})
	if m.input != nil || len(m.tags) != 0 || m.overview == nil {
		t.Errorf("input %+v, tags %v, overview open %v", m.input, m.tags, m.overview != nil)
	}
	m.handle(event{key: keyCancel})
	if m.overview != nil {
		t.Error("Esc again should leave the overview")
	}
}
