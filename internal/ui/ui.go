// Package ui draws the session tree in the terminal and handles keys.
package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
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
	// AllGroups shows every session of a session group, like choose-tree -G,
	// instead of one per group.
	AllGroups bool
	// Zoom zooms the pane the selector runs in while it runs, like
	// choose-tree -Z. It does nothing in a popup.
	Zoom bool
	// Normal starts in normal mode instead of search mode.
	Normal bool
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
	helpTop  int // first help line shown, when the help is scrolled
	// normal is normal mode, where keys are commands as in tmux's chooser;
	// otherwise typing edits the search. Ctrl-/ switches.
	normal    bool
	template  string // Options.Command
	allGroups bool
	order     int // index into SortOrders
	reverse   bool
	screens   map[string]screen
	height    int   // screen height at the last render
	previewY  int   // screen row where the preview starts
	previewH  int   // preview height at the last render; 0 when not shown
	hits      []hit // clickable boxes of the preview
	// The first window shown in a session preview, and for which session.
	winOffset    int
	winOffsetFor *tmux.Session
	// A question waiting for y or n on the prompt line.
	confirm *confirmation
	// A line of input being typed on the prompt line instead of the query.
	input *inputPrompt
	// The right-click menu, while open.
	menu *menu
	// The matrix view, while open.
	matrix *matrixView
	// Tagged rows, by target, so tags survive reloading the tree.
	tags map[string]tmux.Target
	// The last left click, to detect a double click.
	lastClickRow int
	lastClickAt  time.Time
}

// confirmation is a yes/no question; action runs on y.
type confirmation struct {
	question string
	action   func() (done bool, target *tmux.Target, err error)
}

// inputPrompt reads a line on the prompt line; action runs on Enter.
type inputPrompt struct {
	label  string
	text   []rune
	action func(text string) (done bool, target *tmux.Target, err error)
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
		template: o.Command, allGroups: o.AllGroups, normal: o.Normal}
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
	if o.Zoom && o.Inside {
		if unzoom := zoomOwnPane(o.Tmux); unzoom != nil {
			defer unzoom()
		}
	}

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
			if m.matrix == nil && (!m.preview || m.help) {
				continue
			}
			m.screens = map[string]screen{}
		case <-quit:
			return nil, nil
		}
	}
}

// zoomOwnPane zooms the pane whose terminal is ours, if it is not zoomed
// already, and returns a function that unzooms it, or nil.
func zoomOwnPane(tm tmux.Tmux) func() {
	cmd := exec.Command("tty")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	pane, zoomed, err := tm.PaneByTTY(strings.TrimSpace(string(out)))
	if err != nil || pane == "" || zoomed || tm.ToggleZoom(pane) != nil {
		return nil
	}
	return func() {
		// Only undo our zoom: the pane may have been closed or unzoomed since.
		if p, zoomed, _ := tm.PaneByTTY(strings.TrimSpace(string(out))); p == pane && zoomed {
			tm.ToggleZoom(pane)
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
	if !m.allGroups {
		sessions = squashGroups(sessions, here.Session)
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
	if m.matrix != nil {
		return m.matrixKey(ev)
	}
	if m.menu != nil {
		return m.menuKey(ev)
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
	if p := m.input; p != nil {
		switch ev.key {
		case keyRune:
			p.text = append(p.text, ev.r)
		case keyBackspace:
			if len(p.text) > 0 {
				p.text = p.text[:len(p.text)-1]
			}
		case keyDeleteWord:
			p.text = deleteWord(p.text)
		case keyClear:
			p.text = nil
		case keyCancel:
			m.input = nil
		case keyEnter:
			m.input = nil
			return p.action(string(p.text))
		}
		return false, nil, nil
	}
	if m.help {
		// Movement keys and the wheel scroll the help.
		scroll := map[key]int{keyUp: -1, keyDown: 1, keyPageUp: -m.treeH, keyPageDown: m.treeH,
			keyHome: -len(helpText), keyEnd: len(helpText)}
		if d, ok := scroll[ev.key]; ok {
			m.helpTop += d
			return false, nil, nil
		}
		if ev.key == keyMouse {
			switch {
			case ev.press && ev.button&^(4|8|16) == 64:
				m.helpTop--
			case ev.press && ev.button&^(4|8|16) == 65:
				m.helpTop++
			case ev.press && ev.button == 0:
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
	if ev.key == keyToggleMode {
		m.normal = !m.normal
		return false, nil, nil
	}
	if m.normal {
		switch ev.key {
		case keyRune:
			return m.normalKey(ev.r)
		case keyAlt:
			if r := ev.r; r >= 'a' && r <= 'z' {
				return m.shortcut(10 + int(r-'a'))
			}
		case keyClear:
			m.move(-max(1, m.treeH/2))
			return false, nil, nil
		case keyBackspace, keyDeleteWord:
			return false, nil, nil
		}
	}
	switch ev.key {
	case keyRune:
		if len(m.query) == 0 {
			switch ev.r {
			case '?':
				m.help, m.helpTop = true, 0
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
		m.query = deleteWord(m.query)
		m.setQuery()
	case keyTagAll:
		for _, r := range m.rows {
			if !r.context {
				m.tag(r.n, true)
			}
		}
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
	case keyHalfDown:
		m.move(max(1, m.treeH/2))
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
		m.help, m.helpTop = true, 0
	case keyMatrix:
		m.openMatrix()
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

// normalKey handles a plain key in normal mode: tmux's chooser keys, which
// include vim's motions.
func (m *model) normalKey(r rune) (done bool, target *tmux.Target, err error) {
	keys := map[rune]key{'j': keyDown, 'k': keyUp, 'l': keyExpand, '+': keyExpand,
		'h': keyCollapse, '-': keyCollapse, 'g': keyHome, 'G': keyEnd, 'v': keyTogglePreview, '?': keyHelp}
	if k, ok := keys[r]; ok {
		return m.handle(event{key: k})
	}
	switch r {
	case 'q':
		return true, nil, nil
	case '/', 'f':
		m.normal = false
	case 'n':
		m.nextMatch(1)
	case 'N':
		m.nextMatch(-1)
	case '<':
		m.winOffset--
	case '>':
		m.winOffset++
	case 'H':
		return m.command('h')
	case 't', 'T', 'x', 'X', ':', 'm', 'M', 'o', 'O', 'r':
		return m.command(r)
	default:
		if r >= '0' && r <= '9' {
			return m.shortcut(int(r - '0'))
		}
	}
	return false, nil, nil
}

// nextMatch moves the selection to the next row, in direction d, that
// matches the query, wrapping around.
func (m *model) nextMatch(d int) {
	for i := 1; i <= len(m.rows); i++ {
		j := ((m.sel+d*i)%len(m.rows) + len(m.rows)) % len(m.rows)
		if !m.rows[j].context {
			m.sel = j
			return
		}
	}
}

// shortcut jumps to the row labelled with shortcut i on screen.
func (m *model) shortcut(i int) (done bool, target *tmux.Target, err error) {
	if idx := m.top + i; i < m.shortcuts() && idx < len(m.rows) && i < m.treeH {
		return m.jump(m.rows[idx].n.target())
	}
	return false, nil, nil
}

// shortcuts is how many rows, from the top of the tree area, have a shortcut
// key: 0–9 and M-a…M-z in normal mode, as in tmux, but only Alt-0…Alt-9 in
// search mode, where Alt-letters are commands.
func (m *model) shortcuts() int {
	if m.normal {
		return 36
	}
	return 10
}

// shortcutLabel is how row i of the tree area is labelled, or "".
func (m *model) shortcutLabel(i int) string {
	switch {
	case i < 0 || i >= m.shortcuts():
		return ""
	case !m.normal:
		return "M-" + strconv.Itoa(i)
	case i < 10:
		return strconv.Itoa(i)
	}
	return "M-" + string(rune('a'+i-10))
}

// command runs the tmux chooser command for key r: pressed with Alt in
// search mode, or as a plain key in normal mode.
func (m *model) command(r rune) (done bool, target *tmux.Target, err error) {
	if r >= '0' && r <= '9' {
		return m.shortcut(int(r - '0'))
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
	case 't':
		if n := m.selected(); n != nil {
			m.tag(n, !m.tagged(n))
			m.move(1)
		}
	case 'T':
		m.tags = nil
	case 'm':
		if n := m.selected(); n != nil {
			if err := m.tmux.Mark(n.target()); err != nil {
				m.status = err.Error()
			}
			return m.reload(), nil, nil
		}
	case 'M':
		if err := m.tmux.ClearMark(); err != nil {
			m.status = err.Error()
		}
		return m.reload(), nil, nil
	case 'X':
		if targets, what := m.actionTargets(); len(targets) > 0 {
			m.confirm = &confirmation{
				question: "Kill " + what + "?",
				action: func() (bool, *tmux.Target, error) {
					m.tags = nil
					return m.kill(targets)
				},
			}
		}
	case ':', ';':
		if targets, what := m.actionTargets(); len(targets) > 0 {
			m.input = &inputPrompt{
				label:  "command for " + what + ": ",
				action: func(text string) (bool, *tmux.Target, error) { return m.runEach(text, targets) },
			}
		}
	}
	return false, nil, nil
}

func (m *model) tagged(n *node) bool {
	_, ok := m.tags[n.target().String()]
	return ok
}

func (m *model) tag(n *node, on bool) {
	t := n.target()
	if !on {
		delete(m.tags, t.String())
		return
	}
	if m.tags == nil {
		m.tags = map[string]tmux.Target{}
	}
	m.tags[t.String()] = t
}

// actionTargets is what Alt-X and Alt-: act on: the tagged items, panes
// before windows before sessions so that none is gone before its turn, or
// else the selected row. what describes them for a question.
func (m *model) actionTargets() (targets []tmux.Target, what string) {
	if len(m.tags) == 0 {
		if n := m.selected(); n != nil {
			return []tmux.Target{n.target()}, n.describe()
		}
		return nil, ""
	}
	for _, t := range m.tags {
		targets = append(targets, t)
	}
	depth := func(t tmux.Target) int {
		switch {
		case t.Pane != "":
			return 2
		case t.Window != "":
			return 1
		}
		return 0
	}
	sort.Slice(targets, func(i, j int) bool {
		if di, dj := depth(targets[i]), depth(targets[j]); di != dj {
			return di > dj
		}
		return targets[i].String() < targets[j].String()
	})
	what = fmt.Sprintf("%d tagged items", len(targets))
	if len(targets) == 1 {
		what = "1 tagged item"
	}
	return targets, what
}

// runEach runs a command template on each target, then reloads the tree.
func (m *model) runEach(template string, targets []tmux.Target) (done bool, target *tmux.Target, err error) {
	if strings.TrimSpace(template) == "" {
		return false, nil, nil
	}
	for _, t := range targets {
		if err := m.tmux.RunTemplate(template, t); err != nil {
			m.status = err.Error()
			break
		}
	}
	return m.reload(), nil, nil
}

// reload reads the tree again, keeping the selection at the same position.
// done is set when the server has gone, after its last session was killed.
func (m *model) reload() (done bool) {
	idx := m.sel
	if err := m.load(); err != nil {
		return true
	}
	m.refresh()
	m.sel = max(0, min(idx, len(m.rows)-1))
	return false
}

// deleteWord removes the last word, and the spaces after it, from text.
func deleteWord(text []rune) []rune {
	for len(text) > 0 && unicode.IsSpace(text[len(text)-1]) {
		text = text[:len(text)-1]
	}
	for len(text) > 0 && !unicode.IsSpace(text[len(text)-1]) {
		text = text[:len(text)-1]
	}
	return text
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

// kill kills targets, then reloads the tree.
func (m *model) kill(targets []tmux.Target) (done bool, target *tmux.Target, err error) {
	for _, t := range targets {
		if err := m.tmux.Kill(t); err != nil {
			m.status = err.Error()
			break
		}
	}
	return m.reload(), nil, nil
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
	case button == 2 && ev.y >= 2 && ev.y < 2+m.treeH:
		if idx := m.top + ev.y - 2; idx < len(m.rows) {
			m.sel = idx
			m.openMenu(ev.x, ev.y)
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
		m.help, m.helpTop = true, 0
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

// gutterWidth is the width of the shortcut label and tag marker before each
// row (see rowSpans).
const gutterWidth = 5

// arrowColumn is the screen column of a row's expand arrow (see rowContent).
func arrowColumn(n *node) int {
	if n.kind == sessionRow {
		return gutterWidth + 1
	}
	return gutterWidth + 3
}

// render draws the whole screen: prompt, tree and, if on, the preview.
func (m *model) render(width, height int) string {
	var b strings.Builder
	b.WriteString("\x1b[?25l")
	m.height = height
	m.hits = nil
	m.previewH = 0
	if m.matrix != nil {
		m.drawMatrix(&b, width, height)
		return b.String()
	}

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
	if m.help {
		m.drawHelp(&b, 2, width, m.treeH+previewH)
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
			if m.tagged(r.n) {
				base = joinStyles(base, styleTagged)
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
	foot := footerSearch
	if m.normal {
		foot = footerNormal
	}
	drawLine(&b, height-1, width, styleDim, false, span{text: foot})
	if m.menu != nil {
		m.menu.draw(&b, width, height)
	}
	// Normal mode takes no text, so it shows no cursor.
	if !m.normal || m.input != nil {
		fmt.Fprintf(&b, "\x1b[1;%dH\x1b[?25h", m.drawPromptCursor(width))
	}
	return b.String()
}

func (m *model) drawPrompt(b *strings.Builder, width int) {
	query := clean(string(m.query))
	order := SortOrders[m.order]
	if m.reverse {
		order += ", reversed"
	}
	status := fmt.Sprintf("(sort: %s)  [%s]  %d/%d", order, m.matcher.Mode(), m.matches, m.total)
	if len(m.tags) > 0 {
		status = fmt.Sprintf("%d tagged  ", len(m.tags)) + status
	}
	right := span{text: status, style: styleDim}
	switch {
	case m.status != "":
		right = span{text: clean(m.status), style: styleError}
	case m.queryErr != "":
		right = span{text: clean(m.queryErr), style: styleError}
	}
	prompt := span{text: "> ", style: styleCurrent}
	if m.normal {
		prompt = span{text: "NORMAL ", style: styleNormal}
	}
	hint := span{text: query}
	switch {
	case m.input != nil:
		prompt = span{text: clean(m.input.label), style: styleConfirm}
		hint = span{text: clean(string(m.input.text))}
	case m.confirm != nil:
		hint = span{text: clean(m.confirm.question) + " (y/n)", style: styleConfirm}
	case query == "" && m.normal:
		hint = span{text: "keys are commands · / or Ctrl-/ to search · ? for help", style: styleDim}
	case query == "":
		hint = span{text: "type to filter · Ctrl-/ for normal mode · ? for help", style: styleDim}
	}
	gap := max(2, width-textWidth(prompt.text)-textWidth(hint.text)-textWidth(right.text))
	drawLine(b, 0, width, "", false, prompt, hint, span{text: strings.Repeat(" ", gap)}, right)
}

// drawPromptCursor is the 1-based column of the cursor on the prompt line.
func (m *model) drawPromptCursor(width int) int {
	if m.input != nil {
		return min(width, 1+textWidth(clean(m.input.label))+textWidth(string(m.input.text)))
	}
	return min(width, 3+textWidth(string(m.query)))
}

// rowSpans draws row n, which is on line i of the tree area: its shortcut
// label, a + if it is tagged, then its content.
func (m *model) rowSpans(n *node, i int) []span {
	gutter := []span{{text: fmt.Sprintf("%-*s", gutterWidth-1, m.shortcutLabel(i)), style: styleDim}, {text: " "}}
	if m.tagged(n) {
		gutter[1] = span{text: "+", style: styleTagged}
	}
	return append(gutter, m.rowContent(n)...)
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
		if n.session.Group != "" {
			info += ", group " + clean(n.session.Group) + ": " + clean(n.session.GroupList)
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

// footerSearch and footerNormal are the key hints on the bottom line.
const (
	footerSearch = " Enter jump  ↑↓ move  →← fold  ^/ normal mode  ^O matrix  ^R regex  ^V preview  Esc quit  ? help"
	footerNormal = " Enter jump  j/k move  h/l fold  t tag  x kill  : command  ^O matrix  / search  ? help  q quit"
)

// drawHelp draws the help in height rows from screen row y. When it does
// not fit, it scrolls, and the last row says how.
func (m *model) drawHelp(b *strings.Builder, y, width, height int) {
	room := height
	if len(helpText) > height {
		room = height - 1
	}
	m.helpTop = max(0, min(m.helpTop, len(helpText)-room))
	for i := 0; i < room; i++ {
		var s []span
		if m.helpTop+i < len(helpText) {
			s = helpSpans(helpText[m.helpTop+i])
		}
		drawLine(b, y+i, width, "", false, s...)
	}
	if room < height {
		drawLine(b, y+room, width, styleDim, false, span{text: fmt.Sprintf(
			"  ↑↓ PgUp PgDn or wheel to scroll (lines %d–%d of %d); any other key closes",
			m.helpTop+1, m.helpTop+room, len(helpText))})
	}
}

// helpSpans styles a line of helpText, which marks its lines with a
// prefix: "# " a heading, "| " a key and its meaning, "$ " a search example
// and its meaning (key or example and meaning separated by a tab), and
// anything else plain text.
func helpSpans(line string) []span {
	prefix, rest := "", line
	if len(line) >= 2 {
		prefix, rest = line[:2], line[2:]
	}
	left, right, _ := strings.Cut(rest, "\t")
	switch prefix {
	case "# ":
		return []span{{text: "  " + rest, style: styleHeading}}
	case "| ", "$ ":
		style := styleKey
		if prefix == "$ " {
			style = styleExample
		}
		pad := strings.Repeat(" ", max(1, helpKeyWidth-textWidth(left)))
		return []span{{text: "    "}, {text: left, style: style}, {text: pad + right}}
	}
	// Mode names look like the NORMAL indicator on the prompt line.
	spans := []span{{text: "  "}}
	for line != "" {
		i := strings.Index(line, "SEARCH")
		if j := strings.Index(line, "NORMAL"); j >= 0 && (i < 0 || j < i) {
			i = j
		}
		if i < 0 {
			spans = append(spans, span{text: line})
			break
		}
		spans = append(spans, span{text: line[:i]}, span{text: line[i : i+6], style: styleNormal})
		line = line[i+6:]
	}
	return spans
}

// helpKeyWidth is the width of the key column of the help.
const helpKeyWidth = 22

// helpText is shown in place of the tree while help is open; see helpSpans
// for its markup.
var helpText = []string{
	"# Two modes, switched with Ctrl-/",
	"In SEARCH mode (the default) typing filters the list. In NORMAL mode keys",
	"are commands, as in tmux's own chooser, with vim motions.",
	"",
	"# Searching",
	"Every word must match; case is ignored unless the query has an uppercase",
	"letter. Dimmed rows are shown only because something inside them matches.",
	"$ api vim\trows with both words, in any field",
	"$ !zsh\trows without zsh",
	"$ s:api  w:edit\tsession name, window name or index",
	"$ c:vim  d:www\tpane command, pane directory",
	"$ /^api-\\d+$/\ta regular expression (or Ctrl-R to switch)",
	"",
	"# Both modes",
	"| Enter\tjump to the selected row",
	"| Up Down  Ctrl-P/N\tmove",
	"| PgUp PgDn  Ctrl-B/F\tpage; Ctrl-D half a page down",
	"| Home End\tfirst / last row",
	"| Right Left  Tab\texpand / collapse",
	"| Ctrl-T\ttag every match",
	"| Ctrl-R\tswitch between words and regex",
	"| Ctrl-V\tshow or hide the preview",
	"| Ctrl-O\tmatrix view of the selected session (see below)",
	"| F1\tthis help",
	"| Esc\tclose (this help, then the selector)",
	"",
	"# Search mode: letters type, tmux's keys are on Alt",
	"| Alt-0 … Alt-9\tjump to the row labelled M-0 … M-9",
	"| Alt-t  Alt-T\ttag or untag the row / untag all",
	"| Alt-x  Alt-X\tkill the row / kill the tagged rows (asks y/n)",
	"| Alt-:\trun a command on the tagged rows (%% is each one)",
	"| Alt-m  Alt-M\tmark / unmark the pane",
	"| Alt-o  Alt-r\tsort by index, name or time / reverse",
	"| Alt-h\tback to the pane the selector started from",
	"| Alt-+  Alt--\texpand / collapse everything",
	"| Ctrl-W  Ctrl-U\tdelete a word / clear the query",
	"| ?  < >\thelp; scroll a session preview (empty query only)",
	"",
	"# Normal mode: tmux's keys",
	"| j k\tdown / up",
	"| h l  - +\tcollapse / expand",
	"| g G\tfirst / last row",
	"| Ctrl-U  Ctrl-D\thalf a page up / down",
	"| 0 … 9  M-a … M-z\tjump to the row with that label",
	"| t T\ttag or untag the row / untag all",
	"| x X\tkill the row / kill the tagged rows (asks y/n)",
	"| :\trun a command on the tagged rows (%% is each one)",
	"| m M\tmark / unmark the pane",
	"| O r\tsort by index, name or time / reverse",
	"| H\tback to the pane the selector started from",
	"| n N\tnext / previous row matching the search",
	"| v\tshow or hide the preview",
	"| < >\tscroll a session preview",
	"| / f\tback to search mode",
	"| q\tquit",
	"",
	"# Matrix view (Ctrl-O)",
	"All windows of the session at once, live, for watching them. From a",
	"session row they share the screen evenly; from a window row that window",
	"takes the middle half and the others surround it.",
	"| ←↑↓→  h j k l\tmove the focus",
	"| Enter  double-click\tswitch to the window in focus",
	"| click  wheel\tfocus a window / the next or previous one",
	"| o  Ctrl-O\tcentre the window in focus; again to share evenly",
	"| Esc  q\tback to the list",
	"",
	"# Mouse",
	"| click\tselect",
	"| double-click\tjump",
	"| wheel\tmove; over a session preview, scroll its windows",
	"| click an arrow\texpand, collapse, or scroll a session preview",
	"| click the preview\tjump to the pane or window under the pointer",
	"| right-click\ta menu of commands",
	"| click the footer\tthis help",
	"",
	"Arrow keys and the wheel scroll this help; any other key closes it.",
}
