package ui

import (
	"fmt"
	"strconv"

	"github.com/mauricioprado00/tmux-session-selector/internal/filter"
	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

// ovSearch is the overview's search, open from / until Esc or Enter. Panes
// that do not match the query are dimmed, and the focus moves through the
// windows that have a pane that does.
type ovSearch struct {
	query    []rune
	matcher  *filter.Matcher
	queryErr string // invalid regex; the last valid matcher stays
}

// openSearch starts searching in the overview.
func (ov *overview) openSearch() {
	mt, _ := filter.New("", filter.Words)
	ov.search = &ovSearch{matcher: mt}
}

// active is whether the search has a query that dims anything.
func (s *ovSearch) active() bool { return s != nil && !s.matcher.Empty() }

// setQuery parses the query; an invalid regex keeps the last matcher.
func (s *ovSearch) setQuery() {
	mt, err := filter.New(string(s.query), filter.Words)
	s.queryErr = ""
	if err != nil {
		s.queryErr = err.Error()
		return
	}
	s.matcher = mt
}

// paneFields are what a pane is searched by, as a pane row of the list is:
// its session and window, its command, directory and title.
func (m *model) paneFields(s *tmux.Session, w *tmux.Window, p *tmux.Pane) []filter.Field {
	return []filter.Field{
		{Kind: filter.Session, Value: clean(s.Name)},
		{Kind: filter.Window, Value: clean(w.Name)},
		{Kind: filter.Index, Value: strconv.Itoa(w.Index)},
		{Kind: filter.Command, Value: clean(p.Command)},
		{Kind: filter.Path, Value: clean(tilde(p.Path, m.home))},
		{Kind: filter.Title, Value: clean(p.Title)},
	}
}

// paneMatches is whether pane p of window w is a match of the search; with
// no search, or an empty query, every pane is.
func (m *model) paneMatches(w *tmux.Window, p *tmux.Pane) bool {
	ov := m.overview
	if !ov.search.active() {
		return true
	}
	return ov.search.matcher.Match(m.paneFields(ov.session, w, p))
}

// windowMatches is whether any pane of w the overview shows matches.
func (m *model) windowMatches(w *tmux.Window) bool {
	for _, p := range layoutPanes(w) {
		if m.paneMatches(w, p) {
			return true
		}
	}
	return false
}

// matchCount is how many windows of the session match.
func (m *model) matchCount() int {
	n := 0
	for _, w := range m.overview.session.Windows {
		if m.windowMatches(w) {
			n++
		}
	}
	return n
}

// nextWindowMatch moves the focus to the next window, in direction d through the
// windows' order and wrapping around, that matches; from 0, the window in
// focus counts if it matches. Nothing moves when no window matches.
func (m *model) nextWindowMatch(d int) {
	ov := m.overview
	n := len(ov.session.Windows)
	start := 1
	if d == 0 {
		d, start = 1, 0
	}
	for i := start; i <= n; i++ {
		j := ((ov.focus+d*i)%n + n) % n
		if m.windowMatches(ov.session.Windows[j]) {
			ov.focus = j
			return
		}
	}
}

// overviewSearchKey handles a key while the overview's search is open:
// typing edits the query, Tab, Shift-Tab and the arrows go to the next or
// previous matching window, and Esc or Enter close the search, leaving the
// focus where it is.
func (m *model) overviewSearchKey(ev event) (done bool, target *tmux.Target, err error) {
	ov := m.overview
	s := ov.search
	edited := false
	switch ev.key {
	case keyRune:
		s.query = append(s.query, ev.r)
		edited = true
	case keyBackspace:
		if len(s.query) > 0 {
			s.query = s.query[:len(s.query)-1]
			edited = true
		}
	case keyDeleteWord:
		s.query, edited = deleteWord(s.query), true
	case keyClear:
		s.query, edited = nil, true
	case keyTab, keyDown, keyExpand:
		m.nextWindowMatch(1)
	case keyBackTab, keyUp, keyCollapse:
		m.nextWindowMatch(-1)
	case keyCancel, keyEnter:
		ov.search = nil
	case keyToggleTitles:
		m.toggleTitles()
	case keyMouse:
		return m.overviewMouse(ev)
	}
	if edited {
		s.setQuery()
		m.nextWindowMatch(0)
	}
	return false, nil, nil
}

// searchHeader is the overview header's search prompt and its count, or
// error.
func (m *model) searchHeader() (prompt []span, right span) {
	s := m.overview.search
	prompt = []span{{text: "  / ", style: styleCurrent}, {text: clean(string(s.query))}}
	if len(s.query) == 0 {
		prompt = append(prompt, span{text: "type to search the panes", style: styleDim})
	}
	right = span{text: fmt.Sprintf("%d of %d windows match", m.matchCount(), len(m.overview.session.Windows)),
		style: styleDim}
	if s.queryErr != "" {
		right = span{text: clean(s.queryErr), style: styleError}
	}
	return prompt, right
}

// dimPane dims the inside of the box (x0, y0)–(x1, y1), for a pane that
// does not match the search.
func (c *canvas) dimPane(x0, y0, x1, y1 int) {
	for y := max(0, y0+1); y < min(c.h, y1); y++ {
		for x := max(0, x0+1); x < min(c.w, x1); x++ {
			c.cells[y][x].style = joinStyles(c.cells[y][x].style, styleDim)
		}
	}
}
