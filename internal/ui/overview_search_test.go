package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

func searchModel() *model {
	ws := windows(5)
	ws[1].Panes[0].Command = "vim"
	ws[3].Panes[0].Command = "nvim"
	ws[4].Panes[0].Path = "/srv/api"
	s := &tmux.Session{ID: "$1", Name: "work", Windows: ws}
	roots, total := buildTree([]*tmux.Session{s}, tmux.Location{}, "")
	screens := map[string]screen{}
	for _, w := range ws {
		screens[w.Panes[0].ID] = screen{cells: parseScreen("hello\n")}
	}
	m := &model{roots: roots, total: total, screens: screens}
	m.setQuery()
	m.handle(event{key: keyOverview}) // even, focus on w0
	return m
}

func TestOverviewSearch(t *testing.T) {
	m := searchModel()
	ov := m.overview
	typeText := func(s string) {
		for _, r := range s {
			m.handle(event{key: keyRune, r: r})
		}
	}
	typeText("/vim")
	if ov.search == nil || string(ov.search.query) != "vim" {
		t.Fatalf("search = %+v", ov.search)
	}
	if ov.focus != 1 || m.matchCount() != 2 {
		t.Errorf("focus w%d with %d matches, want w1 of 2", ov.focus, m.matchCount())
	}
	m.handle(event{key: keyTab})
	if ov.focus != 3 {
		t.Errorf("Tab: focus w%d, want w3", ov.focus)
	}
	m.handle(event{key: keyTab})
	if ov.focus != 1 {
		t.Errorf("Tab wraps: focus w%d, want w1", ov.focus)
	}
	m.handle(event{key: keyUp})
	if ov.focus != 3 {
		t.Errorf("Up goes back: focus w%d, want w3", ov.focus)
	}

	// Non-matching panes are dimmed; matching ones are not.
	out := m.render(120, 40)
	if !strings.Contains(out, "2 of 5 windows match") {
		t.Errorf("header should count the matches")
	}

	// The search finds directories too, and Backspace edits.
	m.handle(event{key: keyClear})
	typeText("d:api")
	if ov.focus != 4 || m.matchCount() != 1 {
		t.Errorf("d:api: focus w%d, %d matches", ov.focus, m.matchCount())
	}
	// Enter leaves the search where the focus is; keys are commands again.
	m.handle(event{key: keyEnter})
	if ov.search != nil || m.overview == nil || ov.focus != 4 {
		t.Fatalf("after Enter: search %+v, focus w%d", ov.search, ov.focus)
	}
	m.handle(event{key: keyRune, r: 'h'})
	if ov.focus == 4 {
		t.Error("h should move the focus again after the search")
	}
	// Esc leaves the search, not the overview.
	typeText("/zzz")
	if m.matchCount() != 0 {
		t.Errorf("zzz matches %d", m.matchCount())
	}
	m.handle(event{key: keyCancel})
	if ov.search != nil || m.overview == nil {
		t.Error("Esc should close the search only")
	}
}

func TestOverviewSearchDims(t *testing.T) {
	m := searchModel()
	for _, r := range "/nvim" {
		m.handle(event{key: keyRune, r: r})
	}
	ws := m.overview.session.Windows
	if !m.windowMatches(ws[3]) || m.windowMatches(ws[0]) {
		t.Fatal("w3 should match and w0 not")
	}
	// Four windows' "hello" dimmed, one (w3's) not.
	out := m.render(120, 40)
	if dim, all := strings.Count(out, "2mhello"), strings.Count(out, "hello"); dim != 4 || all != 5 {
		t.Errorf("%d of %d hellos dimmed, want 4 of 5", dim, all)
	}
}

func TestOverviewDirs(t *testing.T) {
	m := searchModel()
	m.stateFile = filepath.Join(t.TempDir(), "state")
	m.overview.session.Windows[0].Panes[0].Path = "/home/me/projects/a-rather-long-directory-name/src"
	m.home = "/home/me"
	if strings.Contains(m.render(120, 40), "/srv/api") {
		t.Fatal("directories should start hidden")
	}
	m.handle(event{key: keyRune, r: 'd'})
	out := m.render(120, 40)
	if !strings.Contains(out, " /srv/api ") || readState(m.stateFile)["overview-dirs"] != "on" {
		t.Errorf("after d: shown %v, saved %q", strings.Contains(out, "/srv/api"), readState(m.stateFile)["overview-dirs"])
	}
	// A path too long for its box keeps its end.
	if !strings.Contains(out, "…") || !strings.Contains(out, "name/src ") {
		t.Errorf("long path should be cut from the left")
	}
	m.handle(event{key: keyRune, r: 'd'})
	if strings.Contains(m.render(120, 40), "/srv/api") || readState(m.stateFile)["overview-dirs"] != "off" {
		t.Error("d again should hide them, and remember that")
	}
}
