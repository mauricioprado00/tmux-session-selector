// Package ui draws the session tree in the terminal and handles keys.
package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mauricioprado00/tmux-session-selector/internal/filter"
	"github.com/mauricioprado00/tmux-session-selector/internal/jobs"
	"github.com/mauricioprado00/tmux-session-selector/internal/procinfo"
	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

// minPreviewHeight is the terminal height from which the preview is shown by
// default, until it has been toggled once (see StateFile).
const minPreviewHeight = 30

type Options struct {
	Tmux      tmux.Tmux
	Client    string // client to switch; empty means the calling client
	Inside    bool   // running inside tmux: switch the client instead of attaching
	Query     string
	Regex     bool
	NoPreview bool
	// StateFile remembers settings between runs, such as whether the
	// preview was last shown; "" remembers nothing. See DefaultStateFile.
	StateFile string
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
	// Overview opens straight into the overview of the current session; Esc
	// or q then closes the selector.
	Overview bool
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
	// jobs shows the background processes of each pane (Ctrl-Z); jobCount
	// is how many rows that added.
	jobs      bool
	jobCount  int
	stateFile string
	// titles shows Claude Code session titles, in the list and the
	// overview; Ctrl-L switches it, and it is remembered.
	titles bool
	// sizes are the overview sizes of windows grown or shrunk with + and -,
	// remembered between runs.
	sizes windowSizes
	// dirs shows each pane's working directory in the overview; d switches
	// it, and it is remembered.
	dirs bool
	// claudeDir is Claude Code's directory, for the titles of stopped
	// Claude Code jobs, and proc is /proc, for the files of editors; ""
	// turns either off.
	claudeDir, proc string
	// editorFiles caches what editorFilesOf found, until the next refresh.
	editorFiles map[string]string
	help        bool
	helpTop     int // first help line shown, when the help is scrolled
	// normal is normal mode, where keys are commands as in tmux's chooser;
	// otherwise typing edits the search. Ctrl-/ switches.
	normal bool
	// overviewOnly is set when the selector opened straight into the
	// overview, so leaving it closes the selector.
	overviewOnly bool
	template     string // Options.Command
	self         string // the selector's own pane, when it runs in one
	allGroups    bool
	order        int // index into SortOrders
	reverse      bool
	screens      map[string]screen
	height       int   // screen height at the last render
	previewY     int   // screen row where the preview starts
	previewH     int   // preview height at the last render; 0 when not shown
	hits         []hit // clickable boxes of the preview
	// The first window shown in a session preview, and for which session.
	winOffset    int
	winOffsetFor *tmux.Session
	// A question waiting for y or n on the prompt line.
	confirm *confirmation
	// A line of input being typed on the prompt line instead of the query.
	input *inputPrompt
	// The right-click menu, while open.
	menu *menu
	// The overview, while open.
	overview *overview
	// live keeps the overview up to date through a control-mode client.
	live controlState
	// refreshEvery is Options.Refresh: how often the preview, and the overview
	// without a control-mode client, are captured again; 0 never.
	refreshEvery time.Duration
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
	state := readState(o.StateFile)
	m := &model{tmux: o.Tmux, client: o.Client, inside: o.Inside, home: home, query: []rune(o.Query),
		refreshEvery: o.Refresh,
		template:     o.Command, allGroups: o.AllGroups, normal: o.Normal, stateFile: o.StateFile,
		titles: state["claude-titles"] != "off", sizes: readSizes(state), jobs: state["background-jobs"] == "on",
		dirs: state["overview-dirs"] == "on", proc: "/proc", claudeDir: os.Getenv("CLAUDE_CONFIG_DIR")}
	if m.claudeDir == "" && home != "" {
		m.claudeDir = filepath.Join(home, ".claude")
	}
	if o.Regex {
		m.mode = filter.Regex
	}
	for i, name := range SortOrders {
		if name == o.Sort {
			m.order = i
		}
	}
	m.reverse = o.Reverse
	if o.Inside && m.client == "" {
		// Name the calling client now: once the overview attaches a
		// control-mode client, "the current client" could mean that one.
		if here, err := m.tmux.Current(""); err == nil {
			m.client = here.Client
		}
	}
	tty := terminalName()
	if o.Inside && tty != "" {
		m.self, _, _ = m.tmux.PaneByTTY(tty)
	}
	if o.Inside {
		m.splitStartWindow()
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	m.setQuery()
	m.selectCurrent()
	if o.Overview {
		m.openStartOverview()
	}
	if o.Zoom && m.self != "" {
		if unzoom := zoomOwnPane(o.Tmux, tty); unzoom != nil {
			defer unzoom()
		}
	}

	t, err := openTerminal(true)
	if err != nil {
		return nil, fmt.Errorf("opening terminal: %w", err)
	}
	defer t.restore()
	_, h := t.size()
	m.preview = initialPreview(o.NoPreview, state["preview"], h)

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

	defer m.stopControl()
	// A frame is drawn after every key, and frameGap after panes print in
	// the overview.
	var frame <-chan time.Time
	var drawn time.Time
	redraw := true
	for {
		m.syncControl()
		if redraw {
			t.f.WriteString(m.render(t.size()))
			drawn = time.Now()
		}
		redraw = true
		var news <-chan struct{}
		if m.live.ctl != nil {
			news = m.live.ctl.News
		}
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
			// The overview with a control-mode client is drawn on news.
			if m.overview == nil && (!m.preview || m.help) || m.overview != nil && m.live.ctl != nil {
				redraw = false
				continue
			}
			if m.overview != nil {
				m.refreshOverview()
			}
			m.screens = map[string]screen{}
			m.editorFiles = nil
		case <-news:
			redraw = false
			if m.takeNews() && frame == nil {
				frame = time.After(max(0, frameGap-time.Since(drawn)))
			}
		case <-frame:
			frame = nil
			m.applyNews()
		case <-quit:
			return nil, nil
		}
	}
}

// initialPreview is whether the preview starts shown: never with
// -no-preview, else as it was left last time (saved, "on" or "off"), else
// when the terminal is tall enough.
func initialPreview(noPreview bool, saved string, height int) bool {
	switch {
	case noPreview:
		return false
	case saved == "on" || saved == "off":
		return saved == "on"
	}
	return height >= minPreviewHeight
}

// splitStartWindow splits the window the selector was started from back into
// the windows it was made of, if it is a live window (see makeLiveWindow).
func (m *model) splitStartWindow() {
	sessions, err := m.tmux.Sessions()
	if err != nil {
		return
	}
	here, err := m.tmux.Current(m.client)
	if err != nil {
		return
	}
	if _, here = withoutPane(sessions, m.self, here); here.Window == "" {
		return
	}
	switch split, err := splitLiveWindow(m.tmux, here.Window); {
	case err != nil:
		m.status = "could not split the live window: " + err.Error()
	case split:
		m.status = "split the live window back into its windows"
	}
}

// zoomOwnPane zooms the pane whose terminal is ours, if it is not zoomed
// already, and returns a function that unzooms it, or nil.
func zoomOwnPane(tm tmux.Tmux, tty string) func() {
	pane, zoomed, err := tm.PaneByTTY(tty)
	if err != nil || pane == "" || zoomed || tm.ToggleZoom(pane) != nil {
		return nil
	}
	return func() {
		// Only undo our zoom: the pane may have been closed or unzoomed since.
		if p, zoomed, _ := tm.PaneByTTY(tty); p == pane && zoomed {
			tm.ToggleZoom(pane)
		}
	}
}

// terminalName is the name of the selector's terminal, such as /dev/pts/3,
// or "" if it has none.
func terminalName() string {
	cmd := exec.Command("tty")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// withoutPane leaves the selector's own pane, self, out of sessions: the
// pane, its window if nothing else is in it (such as the temporary window a
// "new-window" binding opens), and its session if that is left empty. If the
// client was on that window, here moves to the window tmux marks as last,
// the one the client came from, which also becomes its session's active one.
func withoutPane(sessions []*tmux.Session, self string, here tmux.Location) ([]*tmux.Session, tmux.Location) {
	if self == "" {
		return sessions, here
	}
	var kept []*tmux.Session
	for _, s := range sessions {
		var windows []*tmux.Window
		for _, w := range s.Windows {
			var panes []*tmux.Pane
			for _, p := range w.Panes {
				if p.ID != self {
					panes = append(panes, p)
				}
			}
			if len(panes) < len(w.Panes) && here.Pane == self {
				here.Pane = ""
			}
			w.Panes = panes
			switch {
			case len(panes) > 0:
				windows = append(windows, w)
			case w.Active:
				// Our window was the session's current one: the one before
				// it, flagged "-", stands in for it.
				for _, last := range s.Windows {
					if strings.Contains(last.Flags, "-") {
						last.Active = true
						last.Flags = strings.Replace(last.Flags, "-", "*", 1)
						if here.Session == s.ID && here.Window == w.ID {
							here.Window = last.ID
							if p := last.ActivePane(); p != nil {
								here.Pane = p.ID
							}
						}
					}
				}
			}
		}
		s.Windows = windows
		if len(windows) > 0 {
			kept = append(kept, s)
		}
	}
	return kept, here
}

// load reads the tree from tmux.
func (m *model) load() error {
	sessions, err := m.tmux.Sessions()
	if err != nil {
		return err
	}
	m.fixActivity(sessions)
	var here tmux.Location
	if m.inside {
		if here, err = m.tmux.Current(m.client); err != nil {
			return err
		}
	}
	sessions, here = withoutPane(sessions, m.self, here)
	if !m.allGroups {
		sessions = squashGroups(sessions, here.Session)
	}
	m.roots, m.total = buildTree(sessions, here, m.home)
	m.editorFiles = nil
	// An editor's files are searchable, as a pane's title is.
	walkTree(m.roots, func(n *node) {
		if n.kind != jobRow && n.pane != nil {
			if files := m.editorFilesOf(n.pane); files != "" {
				n.fields = append(n.fields, filter.Field{Kind: filter.Title, Value: files})
			}
		}
	})
	if m.jobs {
		m.addJobs()
	}
	sortTree(m.roots, m.order, m.reverse)
	m.screens = map[string]screen{}
	return nil
}

// editorFilesOf is the files the editor running in p has open, or "" if p
// is not running one; see procinfo.EditorFiles. It reads two small files of
// /proc, only for panes whose command is an editor, and remembers the
// answer until the next refresh.
func (m *model) editorFilesOf(p *tmux.Pane) string {
	if m.proc == "" || !procinfo.IsEditor(p.Command) {
		return ""
	}
	key := p.ID + " " + strconv.Itoa(p.PID) + " " + p.Command
	if files, ok := m.editorFiles[key]; ok {
		return files
	}
	if m.editorFiles == nil {
		m.editorFiles = map[string]string{}
	}
	files := clean(procinfo.EditorFiles(m.proc, p.PID))
	m.editorFiles[key] = files
	return files
}

// paneTitle is what Ctrl-L shows for pane p, and in which style: the
// session title of Claude Code, or the files of an editor, or "".
func (m *model) paneTitle(p *tmux.Pane) (string, string) {
	if t := claudeTitle(p); t != "" {
		return t, styleTitle
	}
	return m.editorFilesOf(p), styleFile
}

// isClaude is whether a command line runs Claude Code: its program, or the
// script node runs, is named claude.
func isClaude(args string) bool {
	f := strings.Fields(args)
	for i := 0; i < len(f) && i < 2; i++ {
		if filepath.Base(f[i]) == "claude" {
			return true
		}
	}
	return false
}

// addJobs adds the background processes of every pane to the tree.
func (m *model) addJobs() {
	m.jobCount = 0
	found, err := jobs.List(panePIDs(m.roots))
	if err != nil {
		m.status = "could not list processes: " + err.Error()
		return
	}
	m.jobCount = attachJobs(m.roots, found, func(j *jobs.Job) string {
		if !isClaude(j.Args) {
			return ""
		}
		if t := procinfo.ClaudeTitle(m.claudeDir, j.PID); t != "" {
			return "✳ " + t
		}
		return ""
	})
	m.total += m.jobCount
}

// toggleJobs shows or hides the background processes of the panes, without
// reading the tree again, so what is expanded stays expanded, and
// remembers that for the next run.
func (m *model) toggleJobs() {
	m.jobs = !m.jobs
	if m.jobs {
		m.addJobs()
		sortTree(m.roots, m.order, m.reverse)
		if m.jobCount == 0 && m.status == "" {
			m.status = "no background processes in any pane"
		}
	} else {
		detachJobs(m.roots)
		m.total -= m.jobCount
		m.jobCount = 0
	}
	if err := saveSetting(m.stateFile, "background-jobs", onOff(m.jobs)); err != nil {
		m.status = "could not remember the background processes setting: " + err.Error()
	}
	m.refresh()
}

// toggleTitles shows or hides Claude Code session titles, everywhere, and
// remembers that for the next run.
func (m *model) toggleTitles() {
	m.titles = !m.titles
	if err := saveSetting(m.stateFile, "claude-titles", onOff(m.titles)); err != nil {
		m.status = "could not remember the titles setting: " + err.Error()
	}
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
	if m.overview != nil {
		return m.overviewKey(ev)
	}
	// Outside the overview, Tab and Shift-Tab expand and collapse.
	switch ev.key {
	case keyTab:
		ev.key = keyExpand
	case keyBackTab:
		ev.key = keyCollapse
	}
	if m.menu != nil {
		return m.menuKey(ev)
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
			if !r.context && r.n.kind != jobRow {
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
		if err := saveSetting(m.stateFile, "preview", onOff(m.preview)); err != nil {
			m.status = "could not remember the preview setting: " + err.Error()
		}
	case keyToggleJobs:
		m.toggleJobs()
	case keyToggleTitles:
		m.toggleTitles()
	case keyHelp:
		m.help, m.helpTop = true, 0
	case keyOverview:
		m.openOverview()
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
		'h': keyCollapse, '-': keyCollapse, 'g': keyHome, 'G': keyEnd, 'v': keyTogglePreview, '?': keyHelp,
		'z': keyToggleJobs}
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
	// A background process is not a tmux target: killing, tagging or
	// running a command on its row would hit its whole pane.
	if n := m.selected(); n != nil && n.kind == jobRow {
		if strings.ContainsRune("xt:;", r) || r == 'X' && len(m.tags) == 0 {
			m.status = "that is a background process: bring it back with fg in its pane"
			return false, nil, nil
		}
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
	m.stopControl()
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
	m.stopControl()
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

// arrowColumn is the screen column of a row's expand arrow (see rowContent):
// after the current-row marker, two columns of indent per level.
func arrowColumn(n *node) int {
	return gutterWidth + 1 + 2*n.level()
}

// render draws the whole screen: prompt, tree and, if on, the preview.
func (m *model) render(width, height int) string {
	var b strings.Builder
	b.WriteString("\x1b[?25l")
	m.height = height
	m.hits = nil
	m.previewH = 0
	if m.overview != nil {
		m.drawOverview(&b, width, height)
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
	if m.jobs {
		status = fmt.Sprintf("%d background  ", m.jobCount) + status
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
				span{text: "  "})
			s = append(s, m.titleSpans(n.pane)...)
			s = append(s, span{text: n.path, style: stylePath, hl: hl(filter.Path, n.path)})
		}
		return s
	case jobRow:
		state := span{text: "  running", style: styleRunning}
		if n.job.Stopped {
			state = span{text: "  stopped", style: styleStopped}
		}
		s := []span{marker, {text: strings.Repeat(" ", 2*n.level()) + arrow},
			{text: strconv.Itoa(n.job.PID) + " ", style: styleDim},
			{text: n.command, hl: hl(filter.Command, n.command)}}
		if m.titles && n.title != "" {
			s = append(s, span{text: "  "}, span{text: n.title, style: styleTitle, hl: hl(filter.Title, n.title)})
		}
		return append(s, state)
	default:
		pad := strings.Repeat(" ", n.cmdWidth-textWidth(n.command))
		id := n.pane.ID
		if n.pane.Marked {
			id += "M"
		} else {
			id += " "
		}
		s := []span{marker, {text: "    " + arrow + id + " "},
			{text: n.command, hl: hl(filter.Command, n.command)}, {text: pad + "  "}}
		s = append(s, m.titleSpans(n.pane)...)
		return append(s, span{text: n.path, style: stylePath, hl: hl(filter.Path, n.path)})
	}
}

// titleSpans shows what a pane is working on (see paneTitle), then a gap,
// or nothing.
func (m *model) titleSpans(p *tmux.Pane) []span {
	if !m.titles {
		return nil
	}
	title, style := m.paneTitle(p)
	if title == "" {
		return nil
	}
	return []span{{text: title, style: style, hl: m.matcher.Highlight(filter.Title, title)}, {text: "  "}}
}

// claudeTitle is the session title Claude Code keeps in its pane's title,
// such as "✳ Fix the login form", or "" for a pane not running it. It costs
// nothing: pane_title comes with the rest of the tree in one tmux call.
// Claude Code starts its title with ✳, or a braille spinner while it works.
// A shell in the foreground means Claude Code has exited and left its last
// title behind.
func claudeTitle(p *tmux.Pane) string {
	title := clean(p.Title)
	r, size := utf8.DecodeRuneInString(title)
	if !(r == '✳' || r >= 0x2800 && r <= 0x28ff) || len(title) <= size || title[size] != ' ' {
		return ""
	}
	switch strings.TrimPrefix(p.Command, "-") {
	case "bash", "zsh", "fish", "sh", "dash", "ksh", "tcsh", "csh":
		return ""
	}
	return title
}

// footerSearch and footerNormal are the key hints on the bottom line.
const (
	footerSearch = " Enter jump  ↑↓ move  →← fold  ^/ normal mode  ^O overview  ^R regex  ^V preview  ^Z bg jobs  ^L titles  Esc quit  ? help"
	footerNormal = " Enter jump  j/k move  h/l fold  t tag  x kill  : command  ^O overview  z bg jobs  ^L titles  / search  ? help  q quit"
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
	"| Ctrl-V\tshow or hide the preview (remembered for next time)",
	"| Ctrl-Z\tshow or hide each pane's background processes: jobs",
	"|\tstopped with Ctrl-Z, or sent to the background with bg or &",
	"| Ctrl-L\tshow or hide Claude Code session titles, here and in",
	"|\tthe overview (remembered for next time)",
	"| Ctrl-O\toverview of the selected session (see below)",
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
	"| z\tshow or hide background processes (as Ctrl-Z)",
	"| < >\tscroll a session preview",
	"| / f\tback to search mode",
	"| q\tquit",
	"",
	"# Overview (Ctrl-O)",
	"All windows of the session at once, live, for watching them. From a",
	"session row they share the screen evenly; from a window row that window",
	"takes the middle half and the others surround it.",
	"| ←↑↓→  h j k l\tmove the focus; past an edge it wraps around",
	"| Tab  Shift-Tab\tnext / previous window by number",
	"| 0 … 9\tfocus the window with that number (type 1 7 for 17)",
	"| Home  End\tfirst / last window",
	"| Enter  double-click\tswitch to the window in focus",
	"| click  wheel\tfocus a window / the next or previous one",
	"| o  Ctrl-O\tcentre the window in focus; again to share evenly",
	"| /\tsearch: panes that do not match dim; Tab Shift-Tab and",
	"|\tthe arrows go through the matching windows; Esc Enter done",
	"| d\tshow or hide each pane's directory (remembered)",
	"| + - =\tgrow / shrink the window in focus, or back to size 1;",
	"|\tin the centre it has a size of its own (remembered)",
	"| = =\tevery window back to size 1 (= on a window already at 1)",
	"| Ctrl-L\tshow or hide Claude Code session titles",
	"| w\tjoin the windows into one live window laid out like this,",
	"|\tsplit back into them the next time the selector opens there",
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
