package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

func TestClaudeState(t *testing.T) {
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		path = filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("sessions/1.json", `{"sessionId":"11111111-a","status":"waiting","tmux":"work:@1.%1"}`)
	write("sessions/2.json", `{"sessionId":"22222222-b","status":"busy","tmux":"work:@2.%2"}`)
	write("sessions/3.json", `{"sessionId":"33333333-c","status":"waiting","tmux":"other:@9.%9"}`)
	write("sessions/4.json", `{"sessionId":"44444444-d","status":"idle"}`)
	write("jobs/44444444/state.json", `{"sessionId":"44444444-d","state":"working","tempo":"blocked","name":"XBT-1"}`)
	// A pane id from another tmux server, in a window not here.
	write("sessions/5.json", `{"sessionId":"55555555-e","status":"waiting","tmux":"x:@77.%0"}`)

	work := &tmux.Session{ID: "$1", Name: "work", Windows: windows(3)}
	other := &tmux.Session{ID: "$2", Name: "other", Windows: []*tmux.Window{
		{ID: "@9", Index: 0, Name: "o", Width: 80, Height: 24, Panes: []*tmux.Pane{{ID: "%9", Width: 80, Height: 24}}}}}
	roots, total := buildTree([]*tmux.Session{work, other}, tmux.Location{}, "")
	m := &model{roots: roots, total: total, claudeDir: dir, normal: true, screens: map[string]screen{}}
	m.setQuery()
	if !m.readClaude() || m.readClaude() {
		t.Error("readClaude should report a change only the first time")
	}
	out := m.render(200, 30)
	for _, want := range []string{" ⚠ 2 need input + 1 in the background ", " ⚠ needs input ", "● working",
		" a ⚠ next of 2 need input ", " ⚠ 1 needs input ", "  ● 1 working"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	if strings.Contains(out, "○ idle") {
		t.Error("the pane of another server's session should show nothing")
	}

	for _, want := range []string{"@1", "@9", "@1"} {
		m.normalKey('a')
		if n := m.selected(); n == nil || n.window == nil || n.window.ID != want {
			t.Fatalf("a selected %+v, want window %s", n, want)
		}
	}

	m.sel = 0
	m.openOverview()
	out = m.render(200, 40)
	if !strings.Contains(out, " ⚠ 1 needs input here, 1 elsewhere, 1 in the background ") {
		t.Errorf("overview top line: %q", out)
	}
	if !strings.Contains(out, "\x1b[0;"+styleNeedsInputFrame+"m") {
		t.Error("the pane that needs input should be framed in red")
	}
	m.overviewKey(event{key: keyRune, r: 'a'})
	if m.overview.focus != 1 {
		t.Errorf("a focused window %d, want 1", m.overview.focus)
	}
	m.overviewKey(event{key: keyRune, r: 'a'})
	if m.overview == nil || m.overview.session.ID != "$2" {
		t.Errorf("a with none left here should open the other session's overview")
	}
}
