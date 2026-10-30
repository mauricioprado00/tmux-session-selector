// Package ui draws the session tree in the terminal and handles keys.
package ui

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unicode"

	"github.com/mauricioprado00/tmux-session-selector/internal/filter"
	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

// minPreviewHeight is the terminal height from which the preview is shown by
// default.
const minPreviewHeight = 30

type Options struct {
	Tmux      tmux.Tmux
	Client    string // client to switch; empty means the calling client
	Inside    bool   // running inside tmux: switch the client instead of attaching
	Query     string
	Regex     bool
	NoPreview bool
}

type capture struct {
	lines []string
	err   error
}

type model struct {
	tmux     tmux.Tmux
	client   string
	inside   bool
	home     string
	roots    []*node
	total    int
	query    []rune
	mode     filter.Mode
	matcher  *filter.Matcher
	queryErr string // invalid regex; the last valid result stays shown
	status   string // error from the last action, cleared on the next key
	rows     []row
	matches  int
	sel      int
	top      int
	treeH    int
	preview  bool
	captures map[string]capture
}

// Run shows the selector until the user picks a row or cancels. Inside tmux it
// switches the client itself and returns nil; outside it returns the target
// for the caller to attach to once the terminal is restored. A nil target
// and nil error means the user cancelled.
func Run(o Options) (*tmux.Target, error) {
	home, _ := os.UserHomeDir()
	m := &model{tmux: o.Tmux, client: o.Client, inside: o.Inside, home: home, query: []rune(o.Query)}
	if o.Regex {
		m.mode = filter.Regex
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	m.setQuery()
	m.selectCurrent()

	t, err := openTerminal(true)
	if err != nil {
		return nil, fmt.Errorf("opening terminal: %w", err)
	}
	defer t.restore()
	_, h := t.size()
	m.preview = !o.NoPreview && h >= minPreviewHeight

	keys := make(chan []event)
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := t.f.Read(buf)
			if err != nil {
				close(keys)
				return
			}
			keys <- decode(buf[:n])
		}
	}()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)

	for {
		t.f.WriteString(m.render(t.size()))
		select {
		case evs, ok := <-keys:
			if !ok {
				return nil, errors.New("terminal closed")
			}
			for _, ev := range evs {
				if done, target, err := m.handle(ev); done {
					return target, err
				}
			}
		case <-winch:
		case <-quit:
			return nil, nil
		}
	}
}

// load reads the tree from tmux.
func (m *model) load() error {
	sessions, err := m.tmux.Sessions()
	if err != nil {
		return err
	}
	var here tmux.Location
	if m.inside {
		if here, err = m.tmux.Current(m.client); err != nil {
			return err
		}
	}
	m.roots, m.total = buildTree(sessions, here, m.home)
	m.captures = map[string]capture{}
	return nil
}

// setQuery parses the query and refreshes the rows. An invalid regex keeps
// the previous matcher.
func (m *model) setQuery() {
	mt, err := filter.New(string(m.query), m.mode)
	m.queryErr = ""
	if err != nil {
		m.queryErr = err.Error()
		if m.matcher != nil {
			return
		}
		mt, _ = filter.New("", filter.Words)
	}
	m.matcher = mt
	m.refresh()
}

// refresh recomputes the visible rows, keeping the selection on the same row
// or its nearest shown ancestor when it can, else on the first match.
func (m *model) refresh() {
	var prev *node
	if m.sel < len(m.rows) {
		prev = m.rows[m.sel].n
	}
	m.rows, m.matches = visible(m.roots, m.matcher)
	if m.matcher.Empty() {
		m.matches = m.total
	}
	for n := prev; n != nil; n = n.parent {
		if i := m.indexOf(n); i >= 0 && !m.rows[i].context {
			m.sel = i
			return
		}
	}
	m.sel = 0
	for i, r := range m.rows {
		if !r.context {
			m.sel = i
			return
		}
	}
}

func (m *model) indexOf(n *node) int {
	for i, r := range m.rows {
		if r.n == n {
			return i
		}
	}
	return -1
}

// selectCurrent selects the client's current window, or failing that its
// current session.
func (m *model) selectCurrent() {
	best := -1
	for i, r := range m.rows {
		if r.n.current && r.n.kind == windowRow {
			best = i
			break
		}
		if r.n.current && best < 0 {
			best = i
		}
	}
	if best >= 0 && !m.rows[best].context {
		m.sel = best
	}
}

func (m *model) selected() *node {
	if m.sel < len(m.rows) {
		return m.rows[m.sel].n
	}
	return nil
}

func (m *model) move(d int) {
	m.sel = max(0, min(len(m.rows)-1, m.sel+d))
}

// handle applies one key. done ends the program with target and err.
func (m *model) handle(ev event) (done bool, target *tmux.Target, err error) {
	m.status = ""
	switch ev.key {
	case keyRune:
		m.query = append(m.query, ev.r)
		m.setQuery()
	case keyBackspace:
		if len(m.query) > 0 {
			m.query = m.query[:len(m.query)-1]
			m.setQuery()
		}
	case keyDeleteWord:
		q := m.query
		for len(q) > 0 && unicode.IsSpace(q[len(q)-1]) {
			q = q[:len(q)-1]
		}
		for len(q) > 0 && !unicode.IsSpace(q[len(q)-1]) {
			q = q[:len(q)-1]
		}
		m.query = q
		m.setQuery()
	case keyClear:
		m.query = nil
		m.setQuery()
	case keyUp:
		m.move(-1)
	case keyDown:
		m.move(1)
	case keyPageUp:
		m.move(-max(1, m.treeH))
	case keyPageDown:
		m.move(max(1, m.treeH))
	case keyHome:
		m.move(-len(m.rows))
	case keyEnd:
		m.move(len(m.rows))
	case keyExpand:
		if n := m.selected(); n != nil && len(n.children) > 0 && !n.expanded && m.matcher.Empty() {
			n.expanded = true
			m.refresh()
		}
	case keyCollapse:
		n := m.selected()
		switch {
		case n == nil:
		case len(n.children) > 0 && n.expanded && m.matcher.Empty():
			n.expanded = false
			m.refresh()
		case n.parent != nil:
			m.sel = m.indexOf(n.parent)
		}
	case keyToggleRegex:
		if m.mode == filter.Regex {
			m.mode = filter.Words
		} else {
			m.mode = filter.Regex
		}
		m.setQuery()
	case keyTogglePreview:
		m.preview = !m.preview
	case keyCancel:
		return true, nil, nil
	case keyEnter:
		n := m.selected()
		if n == nil {
			break
		}
		t := n.target()
		if !m.inside {
			return true, &t, nil
		}
		if err := m.tmux.Switch(m.client, t); err != nil {
			// The target may have gone away since the list was read.
			m.status = err.Error()
			if err := m.load(); err != nil {
				return true, nil, err
			}
			m.refresh()
			break
		}
		return true, nil, nil
	}
	return false, nil, nil
}

// render draws the whole screen: prompt, tree and, if on, the preview.
func (m *model) render(width, height int) string {
	var b strings.Builder
	b.WriteString("\x1b[?25l")

	previewH := 0
	if m.preview && height >= 12 {
		previewH = height * 2 / 5
	}
	m.treeH = max(1, height-2-previewH)
	if m.sel >= len(m.rows) {
		m.sel = max(0, len(m.rows)-1)
	}
	if m.sel < m.top {
		m.top = m.sel
	}
	if m.sel >= m.top+m.treeH {
		m.top = m.sel - m.treeH + 1
	}
	m.top = max(0, min(m.top, len(m.rows)-m.treeH))

	m.drawPrompt(&b, width)
	drawLine(&b, 1, width, styleDim, false, span{text: strings.Repeat("─", width)})
	for i := 0; i < m.treeH; i++ {
		idx := m.top + i
		switch {
		case idx < len(m.rows):
			r := m.rows[idx]
			base := ""
			if r.context {
				base = styleDim
			}
			if idx == m.sel {
				base = joinStyles(base, styleSelect)
			}
			drawLine(&b, 2+i, width, base, idx == m.sel, m.rowSpans(r.n)...)
		case len(m.rows) == 0 && i == 0:
			drawLine(&b, 2, width, styleDim, false, span{text: "  no matches"})
		default:
			drawLine(&b, 2+i, width, "", false)
		}
	}
	if previewH > 0 {
		m.drawPreview(&b, 2+m.treeH, width, previewH)
	}
	cursor := min(width, 3+textWidth(string(m.query)))
	fmt.Fprintf(&b, "\x1b[1;%dH\x1b[?25h", cursor)
	return b.String()
}

func (m *model) drawPrompt(b *strings.Builder, width int) {
	query := clean(string(m.query))
	right := span{text: fmt.Sprintf("[%s]  %d/%d", m.matcher.Mode(), m.matches, m.total), style: styleDim}
	switch {
	case m.status != "":
		right = span{text: clean(m.status), style: styleError}
	case m.queryErr != "":
		right = span{text: clean(m.queryErr), style: styleError}
	}
	gap := max(2, width-2-textWidth(query)-textWidth(right.text))
	drawLine(b, 0, width, "", false,
		span{text: "> ", style: styleCurrent}, span{text: query}, span{text: strings.Repeat(" ", gap)}, right)
}

func (m *model) rowSpans(n *node) []span {
	marker := span{text: " "}
	if n.current {
		marker = span{text: "*", style: styleCurrent}
	}
	arrow := "  "
	if len(n.children) > 0 {
		if n.expanded || !m.matcher.Empty() {
			arrow = "▾ "
		} else {
			arrow = "▸ "
		}
	}
	hl := m.matcher.Highlight
	switch n.kind {
	case sessionRow:
		info := fmt.Sprintf("  (%d windows", len(n.session.Windows))
		if len(n.session.Windows) == 1 {
			info = "  (1 window"
		}
		if n.session.Attached {
			info += ", attached"
		}
		return []span{marker, {text: arrow}, {text: n.name, hl: hl(filter.Session, n.name)},
			{text: info + ")", style: styleDim}}
	case windowRow:
		idx := fmt.Sprint(n.window.Index)
		s := []span{marker, {text: "  " + arrow}, {text: idx, hl: hl(filter.Index, idx)}, {text: ": "},
			{text: n.name, hl: hl(filter.Window, n.name)}}
		if n.pane != nil {
			s = append(s, span{text: "  "}, span{text: n.command, hl: hl(filter.Command, n.command)},
				span{text: "  "}, span{text: n.path, style: stylePath, hl: hl(filter.Path, n.path)})
		}
		return s
	default:
		pad := strings.Repeat(" ", n.cmdWidth-textWidth(n.command))
		return []span{marker, {text: "      " + n.pane.ID + "  "},
			{text: n.command, hl: hl(filter.Command, n.command)}, {text: pad + "  "},
			{text: n.path, style: stylePath, hl: hl(filter.Path, n.path)}}
	}
}

// drawPreview draws a separator at row y and the end of the selected pane's
// content below it, in height rows in all.
func (m *model) drawPreview(b *strings.Builder, y, width, height int) {
	drawLine(b, y, width, styleDim, false, span{text: strings.Repeat("─", width)})
	var lines []string
	if n := m.selected(); n != nil {
		if p := n.previewPane(); p != nil {
			c := m.capture(p.ID)
			lines = c.lines
			if c.err != nil {
				lines = []string{"preview unavailable: " + c.err.Error()}
			}
		}
	}
	room := height - 1
	if len(lines) > room {
		lines = lines[len(lines)-room:]
	}
	for i := 0; i < room; i++ {
		var s []span
		if i < len(lines) {
			s = []span{{text: clean(lines[i])}}
		}
		drawLine(b, y+1+i, width, "", false, s...)
	}
}

// capture returns a pane's content without trailing blank lines, cached for
// the life of the program.
func (m *model) capture(id string) capture {
	if c, ok := m.captures[id]; ok {
		return c
	}
	out, err := m.tmux.Capture(id)
	lines := strings.Split(out, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	c := capture{lines: lines, err: err}
	m.captures[id] = c
	return c
}
