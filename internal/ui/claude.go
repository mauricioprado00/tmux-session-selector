package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mauricioprado00/tmux-session-selector/internal/claude"
	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

// What Claude Code sessions are doing (see package claude), shown on the
// panes they run in: needs input in red, reversed, to be seen from afar;
// working in yellow; idle dim. Session and window rows, and the overview's
// window labels, show what their panes need; the top line counts the
// sessions that need input, background ones (run by Claude Code's daemon,
// in no pane) included, and the bottom line then offers a (Alt-a while
// searching), which goes to the next pane that needs input. The state is
// read again every second.

// claudeScanEvery is how often the running claude processes are looked at
// again for the directories they keep their sessions in.
const claudeScanEvery = 5 * time.Second

const (
	styleNeedsInput      = "1;7;31"
	styleNeedsInputFrame = "1;31"
	styleWorking         = "33"
)

// claudeState is what the model knows of Claude Code sessions.
type claudeState struct {
	panes map[string]claude.Session // by pane id
	// background are the background sessions, in no pane.
	background []claude.Session
	// key tells whether anything shown changed since the last read.
	key string
}

// readClaude reads the sessions' state again, and returns whether anything
// shown has changed.
func (m *model) readClaude() bool {
	if m.proc != "" && time.Since(m.claudeScanned) >= claudeScanEvery {
		m.claudeDirs, m.claudeScanned = claude.Dirs(m.proc, m.claudeBase...), time.Now()
	}
	sessions := claude.ReadAll(m.claudeDirs, m.proc)
	cs := claudeState{panes: map[string]claude.Session{}}
	var key strings.Builder
	for _, s := range sessions {
		switch {
		case s.Pane != "":
			cs.panes[s.Pane] = s
		case s.Job:
			cs.background = append(cs.background, s)
		default:
			continue
		}
		fmt.Fprintf(&key, "%s %s %s %d %s\n", s.Pane, s.Window, s.ID, s.State, s.Name)
	}
	cs.key = key.String()
	changed := cs.key != m.claude.key
	m.claude = cs
	return changed
}

// claudeOf is the state of the Claude Code session in pane p of window w.
func (m *model) claudeOf(w *tmux.Window, p *tmux.Pane) claude.State {
	if s, ok := m.claude.panes[p.ID]; ok && s.Window == w.ID {
		return s.State
	}
	return claude.None
}

// claudeCount counts the panes of windows that need input and that work.
func (m *model) claudeCount(windows []*tmux.Window) (needs, working int) {
	for _, w := range windows {
		for _, p := range w.Panes {
			switch m.claudeOf(w, p) {
			case claude.NeedsInput:
				needs++
			case claude.Working:
				working++
			}
		}
	}
	return needs, working
}

// claudeSpan is the badge of a pane's session, or "" for none.
func claudeSpan(st claude.State) span {
	switch st {
	case claude.NeedsInput:
		return span{text: " ⚠ needs input ", style: styleNeedsInput}
	case claude.Working:
		return span{text: "● working", style: styleWorking}
	case claude.Idle:
		return span{text: "○ idle", style: styleDim}
	}
	return span{}
}

// claudeSummary is what the panes of windows need, for a session or window
// row: how many need input, and how many work.
func (m *model) claudeSummary(windows []*tmux.Window) []span {
	needs, working := m.claudeCount(windows)
	var s []span
	if needs > 0 {
		s = append(s, span{text: "  "}, span{text: " ⚠ " + needInput(needs) + " ", style: styleNeedsInput})
	}
	if working > 0 {
		s = append(s, span{text: fmt.Sprintf("  ● %d working", working), style: styleWorking})
	}
	return s
}

// needInput is "1 needs input" or "3 need input".
func needInput(n int) string {
	if n == 1 {
		return "1 needs input"
	}
	return fmt.Sprintf("%d need input", n)
}

// claudeNeeds counts the sessions that need input: in the panes of
// sessions, and in the background.
func (m *model) claudeNeeds(sessions []*tmux.Session) (panes, background int) {
	for _, s := range sessions {
		n, _ := m.claudeCount(s.Windows)
		panes += n
	}
	for _, s := range m.claude.background {
		if s.State == claude.NeedsInput {
			background++
		}
	}
	return panes, background
}

// needsSpans is the top line's count of the sessions that need input, or
// nothing.
func needsSpans(panes, background int) []span {
	var text string
	switch {
	case panes > 0 && background > 0:
		text = fmt.Sprintf(" ⚠ %s + %d in the background ", needInput(panes), background)
	case panes > 0:
		text = " ⚠ " + needInput(panes) + " "
	case background > 0:
		text = " ⚠ " + needInput(background) + " in the background (claude agents) "
	default:
		return nil
	}
	return []span{{text: text, style: styleNeedsInput}, {text: "  "}}
}

// needsHint is the bottom line's offer of a, when a pane needs input.
func needsHint(panes int, key string) []span {
	if panes == 0 {
		return nil
	}
	return []span{{text: " " + key + " ⚠ next of " + needInput(panes) + " ", style: styleNeedsInput}}
}

// treeSessions is the sessions of the list.
func (m *model) treeSessions() []*tmux.Session {
	var s []*tmux.Session
	for _, n := range m.roots {
		s = append(s, n.session)
	}
	return s
}

// nextNeedsInput selects the next pane, after the selected row in the
// tree, whose session needs input, opening the rows it is in.
func (m *model) nextNeedsInput() {
	var first, next *node
	passed := m.selected() == nil
	sel := m.selected()
	walkTree(m.roots, func(n *node) {
		if n.kind != jobRow && n.pane != nil && m.claudeOf(n.window, n.pane) == claude.NeedsInput {
			if first == nil {
				first = n
			}
			if passed && next == nil {
				next = n
			}
		}
		if n == sel {
			passed = true
		}
	})
	if first == nil {
		m.status = m.noneNeedsInput()
		return
	}
	n := next
	if n == nil {
		n = first // around from the top
	}
	for p := n.parent; p != nil; p = p.parent {
		p.expanded = true
	}
	m.refresh()
	if i := m.indexOf(n); i >= 0 {
		m.sel = i
	} else {
		m.status = "the pane that needs input is filtered out: " + n.describe()
	}
}

// noneNeedsInput says that no pane needs input, and which background
// sessions do, if any.
func (m *model) noneNeedsInput() string {
	var names []string
	for _, s := range m.claude.background {
		if s.State == claude.NeedsInput {
			names = append(names, s.Name)
		}
	}
	if len(names) > 0 {
		return "no pane needs input; in the background (claude agents): " + strings.Join(names, ", ")
	}
	return "no Claude Code session needs input"
}

// overviewNeedsInput focuses the next window of the overview, after the one
// in focus, with a pane that needs input; with no other in this session, it
// opens the overview of the next session that has one.
func (m *model) overviewNeedsInput() {
	ov := m.overview
	ws := ov.session.Windows
	for i := 1; i < len(ws); i++ {
		w := ws[(ov.focus+i)%len(ws)]
		if n, _ := m.claudeCount([]*tmux.Window{w}); n > 0 {
			ov.focus = (ov.focus + i) % len(ws)
			return
		}
	}
	// The sessions after this one, around.
	at := 0
	for i, n := range m.roots {
		if n.session.ID == ov.session.ID {
			at = i
		}
	}
	for k := 1; k < len(m.roots); k++ {
		s := m.roots[(at+k)%len(m.roots)].session
		for _, w := range s.Windows {
			if c, _ := m.claudeCount([]*tmux.Window{w}); c > 0 {
				m.overviewOf(s.ID, w.ID)
				return
			}
		}
	}
	if n, _ := m.claudeCount(ws[ov.focus : ov.focus+1]); n == 0 {
		m.status = m.noneNeedsInput()
	}
}

// drawClaude frames the panes of w whose session needs input in red and
// writes each Claude Code pane's state on its bottom border, in the
// window's box.
func (m *model) drawClaude(c *canvas, w *tmux.Window, box cellBox, frames bool) {
	for _, r := range paneRects(w, box.x0, box.y0, box.x1, box.y1) {
		st := m.claudeOf(w, r.p)
		switch {
		case st == claude.None:
		case frames:
			if st == claude.NeedsInput {
				c.restyleBox(r.x0, r.y0, r.x1, r.y1, styleNeedsInputFrame)
			}
		default:
			b := claudeSpan(st)
			if st != claude.NeedsInput {
				b.text = " " + b.text + " "
			}
			c.text(r.x0+2, r.y1, r.x1-1, b.text, b.style)
		}
	}
}

// overviewOf opens the overview of session sid centred on window wid, in
// place of the one open, as if from the window's row in the list.
func (m *model) overviewOf(sid, wid string) {
	only := m.overviewOnly
	m.closeOverview() // which may read the list again
	m.overviewOnly = only
	for _, sn := range m.roots {
		for _, wn := range sn.children {
			if sn.session.ID != sid || wn.window.ID != wid {
				continue
			}
			sn.expanded = true
			m.refresh()
			if i := m.indexOf(wn); i >= 0 {
				m.sel = i
				m.openOverview()
				return
			}
		}
	}
	m.status = "the window that needs input is filtered out of the list"
}
