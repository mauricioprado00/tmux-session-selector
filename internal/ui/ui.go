// Package ui draws the session tree in the terminal and handles keys.
package ui

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
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
	// Refresh is how often the preview is captured again; 0 keeps the
	// first capture of each pane for the whole run.
	Refresh time.Duration
	// Command, when set, is run on the chosen target instead of switching to
	// it: a tmux command line where the first %% and every %1 become the
	// target, as in choose-tree's template.
	Command string
	// Sort is one of SortOrders; empty means "index".
	Sort    string
	Reverse bool
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
	help     bool
	template string // Options.Command
	order    int    // index into SortOrders
	reverse  bool
	screens  map[string]screen
	height   int   // screen height at the last render
	previewY int   // screen row where the preview starts
	previewH int   // preview height at the last render; 0 when not shown
	hits     []hit // clickable boxes of the preview
	// The first window shown in a session preview, and for which session.
	winOffset    int
	winOffsetFor *tmux.Session
	// A question waiting for y or n on the prompt line.
	confirm *confirmation
	// The last left click, to detect a double click.
	lastClickRow int
	lastClickAt  time.Time
}

// confirmation is a yes/no question; action runs on y.
type confirmation struct {
	question string
	action   func() (done bool, target *tmux.Target, err error)
}

// doubleClick is the longest gap between the clicks of a double click.
const doubleClick = 400 * time.Millisecond

// hit is a box of the preview that, when clicked, jumps to target or, for a
// scroll arrow, scrolls a session preview by scroll windows. Coordinates are
// 0-based screen cells, inclusive.
type hit struct {
	x0, y0, x1, y1 int
	target         tmux.Target
	scroll         int
}

// Run shows the selector until the user picks a row or cancels. Inside tmux it
// switches the client itself and returns nil; outside it returns the target
// for the caller to attach to once the terminal is restored. A nil target
// and nil error means the user cancelled.
func Run(o Options) (*tmux.Target, error) {
	home, _ := os.UserHomeDir()
	m := &model{tmux: o.Tmux, client: o.Client, inside: o.Inside, home: home, query: []rune(o.Query),
		template: o.Command}
	if o.Regex {
		m.mode = filter.Regex
	}
	for i, name := range SortOrders {
		if name == o.Sort {
			m.order = i
		}
	}
	m.reverse = o.Reverse
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
	var tick <-chan time.Time
	if o.Refresh > 0 {
		ticker := time.NewTicker(o.Refresh)
		defer ticker.Stop()
		tick = ticker.C
	}

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
		case <-tick:
			if !m.preview || m.help {
				continue
			}
			m.screens = map[string]screen{}
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
	sortTree(m.roots, m.order, m.reverse)
	m.screens = map[string]screen{}
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
	if ev.key != keyMouse || ev.press {
		m.status = ""
	}
	if c := m.confirm; c != nil {
		if ev.key == keyMouse && !ev.press {
			return false, nil, nil
		}
		m.confirm = nil
		if ev.key == keyRune && (ev.r == 'y' || ev.r == 'Y') {
			return c.action()
		}
		return false, nil, nil
	}
	if m.help {
		if ev.key == keyMouse {
			if ev.press && ev.button == 0 {
				m.help = false
			}
			return false, nil, nil
		}
		// Any key closes the help. Other than Esc, Enter and F1, it also acts.
		m.help = false
		if ev.key == keyCancel || ev.key == keyEnter || ev.key == keyHelp {
			return false, nil, nil
		}
	}
	switch ev.key {
	case keyRune:
		if len(m.query) == 0 {
			switch ev.r {
			case '?':
				m.help = true
				return false, nil, nil
			case '<':
				m.winOffset--
				return false, nil, nil
			case '>':
				m.winOffset++
				return false, nil, nil
			}
		}
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
	case keyHelp:
		m.help = true
	case keyCancel:
		return true, nil, nil
	case keyEnter:
		if n := m.selected(); n != nil {
			return m.jump(n.target())
		}
	case keyMouse:
		return m.mouse(ev)
	case keyAlt:
		return m.command(ev.r)
	}
	return false, nil, nil
}

// command runs the tmux chooser command for key r, pressed with Alt.
func (m *model) command(r rune) (done bool, target *tmux.Target, err error) {
	switch {
	case r >= '0' && r <= '9':
		if i := m.top + int(r-'0'); i < len(m.rows) && i < m.top+m.treeH {
			return m.jump(m.rows[i].n.target())
		}
		return false, nil, nil
	}
	switch r {
	case 'o', 'O':
		m.order = (m.order + 1) % len(SortOrders)
		sortTree(m.roots, m.order, m.reverse)
		m.refresh()
	case 'h', 'H':
		m.selectStart()
	case '+', '=':
		setExpanded(m.roots, true)
		m.refresh()
	case '-':
		setExpanded(m.roots, false)
		m.refresh()
	case 'r':
		m.reverse = !m.reverse
		sortTree(m.roots, m.order, m.reverse)
		m.refresh()
	case 'x':
		if n := m.selected(); n != nil {
			t := n.target()
			m.confirm = &confirmation{
				question: "Kill " + n.describe() + "?",
				action:   func() (bool, *tmux.Target, error) { return m.kill([]tmux.Target{t}) },
			}
		}
	}
	return false, nil, nil
}

// setExpanded expands or collapses every row of the tree.
func setExpanded(nodes []*node, expanded bool) {
	for _, n := range nodes {
		n.expanded = expanded
		setExpanded(n.children, expanded)
	}
}

// selectStart selects the row of the pane the selector was started from,
// expanding its window and clearing the query if they hide it.
func (m *model) selectStart() {
	var start *node
	for nodes := m.roots; nodes != nil; {
		var next []*node
		for _, n := range nodes {
			if n.current {
				start, next = n, n.children
			}
		}
		nodes = next
	}
	if start == nil {
		return // not started from inside tmux
	}
	for p := start.parent; p != nil; p = p.parent {
		p.expanded = true
	}
	m.refresh()
	if i := m.indexOf(start); i < 0 || m.rows[i].context {
		m.query = nil
		m.setQuery()
	}
	m.sel = m.indexOf(start)
}

// kill kills targets, then reloads the tree, keeping the selection at the
// same position.
func (m *model) kill(targets []tmux.Target) (done bool, target *tmux.Target, err error) {
	idx := m.sel
	for _, t := range targets {
		if err := m.tmux.Kill(t); err != nil {
			m.status = err.Error()
			break
		}
	}
	if err := m.load(); err != nil {
		return true, nil, nil // the last session went, and the server with it
	}
	m.refresh()
	m.sel = max(0, min(idx, len(m.rows)-1))
	return false, nil, nil
}

// jump acts on the chosen target t: it runs the command template if there
// is one, else switches the client to t, or outside tmux returns t for the
// caller to attach to. On failure the error is shown and the list reloaded.
func (m *model) jump(t tmux.Target) (done bool, target *tmux.Target, err error) {
	var failed error
	switch {
	case m.template != "":
		failed = m.tmux.RunTemplate(m.template, t)
	case !m.inside:
		return true, &t, nil
	default:
		failed = m.tmux.Switch(m.client, t)
	}
	if err := failed; err != nil {
		// The target may have gone away since the list was read.
		m.status = err.Error()
		if err := m.load(); err != nil {
			return true, nil, err
		}
		m.refresh()
		return false, nil, nil
	}
	return true, nil, nil
}

// mouse handles a mouse report the way tmux's chooser does: a click selects
// a row, a double click jumps to it, the wheel moves the selection, and a
// click in the preview jumps to the pane or window under it.
func (m *model) mouse(ev event) (done bool, target *tmux.Target, err error) {
	button := ev.button &^ (4 | 8 | 16) // ignore Shift, Meta and Ctrl
	switch {
	case !ev.press:
	case button == 64 || button == 65:
		d := 1
		if button == 64 {
			d = -1
		}
		// Over a session preview the wheel scrolls its windows.
		if n := m.selected(); n != nil && n.kind == sessionRow &&
			ev.y >= m.previewY && ev.y < m.previewY+m.previewH {
			m.winOffset += d
		} else {
			m.move(d)
		}
	case button != 0:
	case ev.y >= 2 && ev.y < 2+m.treeH:
		idx := m.top + ev.y - 2
		if idx >= len(m.rows) {
			break
		}
		double := idx == m.lastClickRow && time.Since(m.lastClickAt) < doubleClick
		m.lastClickRow, m.lastClickAt = idx, time.Now()
		m.sel = idx
		n := m.rows[idx].n
		if double {
			m.lastClickRow = -1
			return m.jump(n.target())
		}
		if len(n.children) > 0 && ev.x == arrowColumn(n) && m.matcher.Empty() {
			n.expanded = !n.expanded
			m.refresh()
		}
	case ev.y == m.height-1:
		m.help = true
	default:
		for i := len(m.hits) - 1; i >= 0; i-- {
			h := m.hits[i]
			if ev.x >= h.x0 && ev.x <= h.x1 && ev.y >= h.y0 && ev.y <= h.y1 {
				if h.scroll != 0 {
					m.winOffset += h.scroll
					return false, nil, nil
				}
				return m.jump(h.target)
			}
		}
	}
	return false, nil, nil
}

// flagSpans styles tmux window flags, making bell and activity stand out.
func flagSpans(flags string) []span {
	var s []span
	for _, r := range clean(flags) {
		style := styleDim
		switch r {
		case '!':
			style = styleBell
		case '#', '~':
			style = styleActivity
		}
		s = append(s, span{text: string(r), style: style})
	}
	return s
}

// arrowColumn is the screen column of a row's expand arrow (see rowSpans).
func arrowColumn(n *node) int {
	if n.kind == sessionRow {
		return 3
	}
	return 5
}

// shortcuts is how many rows, from the top of the tree area, get an Alt-digit
// key that jumps to them.
const shortcuts = 10

// render draws the whole screen: prompt, tree and, if on, the preview.
func (m *model) render(width, height int) string {
	var b strings.Builder
	b.WriteString("\x1b[?25l")
	m.height = height
	m.hits = nil
	m.previewH = 0

	previewH := 0
	if m.preview && height >= 12 {
		previewH = height / 2
	}
	m.treeH = max(1, height-3-previewH)
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
	for i := 0; m.help && i < m.treeH+previewH; i++ {
		var s []span
		if i < len(helpText) {
			s = []span{{text: helpText[i]}}
		}
		drawLine(&b, 2+i, width, "", false, s...)
	}
	for i := 0; !m.help && i < m.treeH; i++ {
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
			drawLine(&b, 2+i, width, base, idx == m.sel, m.rowSpans(r.n, i)...)
		case len(m.rows) == 0 && i == 0:
			drawLine(&b, 2, width, styleDim, false, span{text: "  no matches"})
		default:
			drawLine(&b, 2+i, width, "", false)
		}
	}
	if previewH > 0 && !m.help {
		m.previewH = previewH
		m.drawPreview(&b, 2+m.treeH, width, previewH)
	}
	drawLine(&b, height-1, width, styleDim, false, span{text: footer})
	cursor := min(width, 3+textWidth(string(m.query)))
	fmt.Fprintf(&b, "\x1b[1;%dH\x1b[?25h", cursor)
	return b.String()
}

func (m *model) drawPrompt(b *strings.Builder, width int) {
	query := clean(string(m.query))
	order := SortOrders[m.order]
	if m.reverse {
		order += ", reversed"
	}
	right := span{text: fmt.Sprintf("(sort: %s)  [%s]  %d/%d", order, m.matcher.Mode(), m.matches, m.total), style: styleDim}
	switch {
	case m.status != "":
		right = span{text: clean(m.status), style: styleError}
	case m.queryErr != "":
		right = span{text: clean(m.queryErr), style: styleError}
	}
	hint := span{text: query}
	switch {
	case m.confirm != nil:
		hint = span{text: clean(m.confirm.question) + " (y/n)", style: styleConfirm}
	case query == "":
		hint = span{text: "type to filter · ? for help", style: styleDim}
	}
	gap := max(2, width-2-textWidth(hint.text)-textWidth(right.text))
	drawLine(b, 0, width, "", false,
		span{text: "> ", style: styleCurrent}, hint, span{text: strings.Repeat(" ", gap)}, right)
}

// rowSpans draws row n, which is on line i of the tree area.
func (m *model) rowSpans(n *node, i int) []span {
	shortcut := span{text: "  "}
	if i >= 0 && i < shortcuts {
		shortcut = span{text: strconv.Itoa(i) + " ", style: styleDim}
	}
	spans := m.rowContent(n)
	return append([]span{shortcut}, spans...)
}

func (m *model) rowContent(n *node) []span {
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
		s = append(s, flagSpans(n.window.Flags)...)
		if n.pane == nil {
			s = append(s, span{text: fmt.Sprintf("  (%d panes)", len(n.window.Panes)), style: styleDim})
		} else {
			s = append(s, span{text: "  "}, span{text: n.command, hl: hl(filter.Command, n.command)},
				span{text: "  "}, span{text: n.path, style: stylePath, hl: hl(filter.Path, n.path)})
		}
		return s
	default:
		pad := strings.Repeat(" ", n.cmdWidth-textWidth(n.command))
		id := n.pane.ID
		if n.pane.Marked {
			id += "M"
		} else {
			id += " "
		}
		return []span{marker, {text: "      " + id + " "},
			{text: n.command, hl: hl(filter.Command, n.command)}, {text: pad + "  "},
			{text: n.path, style: stylePath, hl: hl(filter.Path, n.path)}}
	}
}

// footer is the key hint on the bottom line.
const footer = " Enter jump  ↑↓ move  →← expand/collapse  ^R regex  ^V preview  Esc quit  ? help"

// helpText is shown in place of the tree while help is open.
var helpText = []string{
	"  Filter by typing. Every word must match; matching ignores case unless",
	"  the query has an uppercase letter. Dimmed rows only contain a match.",
	"",
	"    api vim       rows with both words, in any field",
	"    !zsh          rows without zsh",
	"    s:api         session name contains api",
	"    w:edit        window name or index contains edit",
	"    c:vim         pane command contains vim",
	"    d:www         pane directory contains www",
	"    !c:zsh        prefixes combine with !",
	"    /^api-\\d+$/   a regular expression (or Ctrl-R to switch modes)",
	"",
	"  Enter          jump to the selected row",
	"  Alt-0 … Alt-9  jump to the row with that number on the left",
	"  Up/Down        move (also Ctrl-P/N, Ctrl-K/J, PgUp/PgDn, Home/End)",
	"  Right/Left     expand, collapse (also Tab/Shift-Tab)",
	"  Ctrl-W/Ctrl-U  delete a word, clear the query",
	"  Ctrl-R         switch between words and regex",
	"  Ctrl-V         show or hide the preview",
	"  ? or F1        this help (? only when the query is empty)",
	"  Alt-x          kill the selected session, window or pane (asks y/n)",
	"  Alt-o / Alt-r  sort by index, name or time (recent first); reverse",
	"  Alt-h          back to the pane the selector was started from",
	"  Alt-+ / Alt--  expand / collapse everything (Alt-= also expands)",
	"  < >            scroll the windows of a session preview (empty query)",
	"  Esc            close (this help, then the selector)",
	"",
	"  Mouse: click selects, double-click jumps, the wheel moves (over a",
	"  session preview it scrolls the windows), a click on an arrow expands,",
	"  collapses or scrolls, a click in the preview jumps to the pane or",
	"  window under it, and a click on the bottom line opens this help.",
	"",
	"  Press any key to close this help.",
}
