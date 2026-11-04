package ui

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

const (
	styleFocus      = "1;33"
	styleFocusLabel = "1;7;33"
	// overviewRing is the smallest grid, in cells per side, of a centred
	// overview: the window in focus takes its middle half, the others the
	// ring around it.
	overviewRing = 4
)

// overview shows every window of a session at once, live, for watching
// them. With a centre window, that window takes the middle half of the
// screen each way and the others surround it; otherwise all windows share
// the screen evenly.
type overview struct {
	session *tmux.Session
	center  *tmux.Window // nil for an even grid
	focus   int          // index into session.Windows
	cells   []cellBox    // where each window was drawn, by index into session.Windows
	// The focus point: where in the focused window the focus is, in screen
	// cells, so that moving through a large window keeps to the column (or
	// row) it was entered by. Outside the focused window it is reset to the
	// window's middle.
	px, py int
	// The size of the area the windows were last drawn in.
	width, height int
	// changed is set when windows opened or closed while the view was open,
	// so the list is read again on the way back to it.
	changed bool
	// The window number being typed, and when its last digit came.
	number   string
	numberAt time.Time
	// search is the search, while / has it open.
	search *ovSearch
	// typing is type mode, while i has it on: keys go to a pane.
	typing *typing
}

// numberGap is the longest pause between the digits of one window number.
const numberGap = time.Second

// cellBox is a box on screen, corners inclusive.
type cellBox struct {
	x0, y0, x1, y1 int
}

func (b cellBox) contains(x, y int) bool {
	return x >= b.x0 && x <= b.x1 && y >= b.y0 && y <= b.y1
}

// openOverview opens the overview of the selected row's session: centred on
// the selected window, or even for a session row.
func (m *model) openOverview() {
	n := m.selected()
	if n == nil || len(n.session.Windows) == 0 {
		return
	}
	ov := &overview{session: n.session}
	focus := n.session.ActiveWindow()
	if n.kind != sessionRow {
		ov.center, focus = n.window, n.window
	}
	for i, w := range n.session.Windows {
		if w == focus {
			ov.focus = i
		}
	}
	m.overview = ov
}

// overviewKey handles an event while the overview is open.
func (m *model) overviewKey(ev event) (done bool, target *tmux.Target, err error) {
	ov := m.overview
	if m.input != nil {
		return m.inputKey(ev)
	}
	if ov.search != nil {
		return m.overviewSearchKey(ev)
	}
	dirs := map[key][2]int{keyUp: {0, -1}, keyDown: {0, 1}, keyExpand: {1, 0}, keyCollapse: {-1, 0}}
	letters := map[rune][2]int{'k': {0, -1}, 'j': {0, 1}, 'l': {1, 0}, 'h': {-1, 0}}
	if d, ok := dirs[ev.key]; ok {
		ov.move(d[0], d[1])
		return false, nil, nil
	}
	n := len(ov.session.Windows)
	switch ev.key {
	case keyTab:
		ov.focus = (ov.focus + 1) % n
	case keyBackTab:
		ov.focus = (ov.focus + n - 1) % n
	case keyHome:
		ov.focus = 0
	case keyEnd:
		ov.focus = n - 1
	case keyRune:
		switch d, ok := letters[ev.r]; {
		case ok:
			ov.move(d[0], d[1])
		case ev.r >= '0' && ev.r <= '9':
			ov.typeDigit(ev.r)
		case ev.r == 'w':
			m.askLiveWindow()
		case ev.r == 'i':
			m.startTyping()
		case ev.r == 't' || ev.r == 'T' || ev.r == 'x' || ev.r == 'X' || ev.r == ':' || ev.r == ';':
			m.overviewCommand(ev.r)
		case ev.r == 'o':
			ov.toggleCenter()
		case ev.r == '/':
			ov.openSearch()
		case ev.r == 'd':
			m.dirs = !m.dirs
			if err := saveSetting(m.stateFile, "overview-dirs", onOff(m.dirs)); err != nil {
				m.status = "could not remember the directories setting: " + err.Error()
			}
		case ev.r == '+':
			m.resizeFocus(1, false)
		case ev.r == '-':
			m.resizeFocus(-1, false)
		case ev.r == '=':
			m.resizeFocus(0, true)
		case ev.r == 'q':
			return m.leaveOverview()
		}
	case keyOverview:
		ov.toggleCenter()
	case keyToggleTitles:
		m.toggleTitles()
	case keyCancel:
		return m.leaveOverview()
	case keyEnter:
		return m.overviewJump()
	case keyMouse:
		return m.overviewMouse(ev)
	}
	return false, nil, nil
}

// typeDigit adds a digit to the window number being typed and focuses the
// window with that index, if there is one. Digits more than numberGap apart
// start a new number.
func (ov *overview) typeDigit(r rune) {
	if time.Since(ov.numberAt) > numberGap {
		ov.number = ""
	}
	ov.number += string(r)
	ov.numberAt = time.Now()
	for i, w := range ov.session.Windows {
		if fmt.Sprint(w.Index) == ov.number {
			ov.focus = i
			return
		}
	}
	// No such window: start again from this digit.
	ov.number = string(r)
	for i, w := range ov.session.Windows {
		if fmt.Sprint(w.Index) == ov.number {
			ov.focus = i
		}
	}
}

// openStartOverview opens straight into the overview of the current
// session, or else the first one, for --overview; leaving it then closes the
// selector.
func (m *model) openStartOverview() {
	n := m.selected()
	for n != nil && n.parent != nil {
		n = n.parent
	}
	if n != nil {
		m.sel, m.overviewOnly = m.indexOf(n), true
		m.openOverview()
	}
}

// leaveOverview goes back to the list, or closes the selector if it opened
// straight into the overview.
func (m *model) leaveOverview() (done bool, target *tmux.Target, err error) {
	if m.overviewOnly {
		return true, nil, nil
	}
	m.closeOverview()
	return false, nil, nil
}

// closeOverview goes back to the list, reading it again if windows opened or
// closed meanwhile.
func (m *model) closeOverview() {
	changed := m.overview.changed
	m.overview = nil
	m.stopControl() // before reading the list: it counts as attached
	if changed {
		m.reload()
	}
}

// refreshOverview reads the session again, so that windows opened or closed
// since the last look appear or go. The focus and the centre stay on the same
// windows; if the centre window closed, the windows share the screen evenly,
// and if the whole session went, the view closes with a note.
func (m *model) refreshOverview() {
	ov := m.overview
	sessions, err := m.tmux.Sessions()
	if err != nil {
		return
	}
	m.fixActivity(sessions)
	sessions, _ = withoutPane(sessions, m.self, tmux.Location{})
	m.pruneTags(sessions)
	if ty := ov.typing; ty != nil && !hasPane(sessions, ty.pane) {
		ov.typing = nil
		m.status = "the pane typed into has gone"
	}
	var s *tmux.Session
	for _, x := range sessions {
		if x.ID == ov.session.ID {
			s = x
		}
	}
	if s == nil || len(s.Windows) == 0 {
		ov.changed = true
		m.closeOverview()
		m.status = "the session has gone"
		return
	}
	ids := func(ws []*tmux.Window) string {
		var b strings.Builder
		for _, w := range ws {
			b.WriteString(w.ID)
		}
		return b.String()
	}
	if ids(s.Windows) != ids(ov.session.Windows) {
		ov.changed = true
	}
	var focusID, centerID string
	if ov.focus < len(ov.session.Windows) {
		focusID = ov.session.Windows[ov.focus].ID
	}
	if ov.center != nil {
		centerID = ov.center.ID
	}
	ov.session, ov.center = s, nil
	ov.focus = min(ov.focus, len(s.Windows)-1)
	for i, w := range s.Windows {
		if w.ID == focusID {
			ov.focus = i
		}
		if w.ID == centerID {
			ov.center = w
		}
	}
}

// resizeFocus grows (d = 1) or shrinks (d = -1) the window in focus by a
// step, or with reset gives it size 1 again: its middle size if it is the
// centre window, else its normal size. A reset of a window already at 1
// resets every window of the session, both sizes, as vim's Ctrl-W = makes
// all windows equal; so = = resets everything. The sizes are saved for next
// time.
func (m *model) resizeFocus(d int, reset bool) {
	ov := m.overview
	w := ov.session.Windows[ov.focus]
	if m.sizes == nil {
		m.sizes = windowSizes{}
	}
	if reset && m.sizes.of(w, w == ov.center) == 1 {
		for _, x := range ov.session.Windows {
			delete(m.sizes, x.ID)
		}
		m.status = "every window back to size 1"
	} else {
		m.sizes.change(w.ID, w == ov.center, d, reset)
	}
	// Every window the selector knows of is still open; sizes of the others
	// are dropped.
	live := map[string]bool{}
	walkTree(m.roots, func(n *node) {
		if n.window != nil {
			live[n.window.ID] = true
		}
	})
	for _, x := range ov.session.Windows {
		live[x.ID] = true
	}
	for id := range m.sizes {
		if !live[id] {
			delete(m.sizes, id)
		}
	}
	if err := updateState(m.stateFile, func(state map[string]string) { writeSizes(state, m.sizes, live) }); err != nil {
		m.status = "could not remember the sizes: " + err.Error()
	}
}

// toggleCenter puts the window in focus in the centre, or, if it is there
// already, goes back to sharing the screen evenly.
func (ov *overview) toggleCenter() {
	focused := ov.session.Windows[ov.focus]
	if ov.center == focused {
		ov.center = nil
	} else {
		ov.center = focused
	}
}

// overviewJump switches to the window in focus, as choosing it in the list
// would. On failure the overview closes so the error shows on the list.
func (m *model) overviewJump() (done bool, target *tmux.Target, err error) {
	ov := m.overview
	w := ov.session.Windows[ov.focus]
	done, target, err = m.jump(tmux.Target{Session: ov.session.ID, Window: w.ID})
	if !done {
		m.overview = nil
	}
	return done, target, err
}

// overviewMouse focuses the window clicked, or switches to it on a double
// click; the wheel moves the focus through the windows in order.
func (m *model) overviewMouse(ev event) (done bool, target *tmux.Target, err error) {
	ov := m.overview
	button := ev.button &^ (4 | 8 | 16)
	n := len(ov.session.Windows)
	switch {
	case !ev.press:
	case button == 64:
		ov.focus = (ov.focus + n - 1) % n
	case button == 65:
		ov.focus = (ov.focus + 1) % n
	case button == 0:
		for i, c := range ov.cells {
			if c.contains(ev.x, ev.y) {
				double := i == m.lastClickRow && time.Since(m.lastClickAt) < doubleClick
				m.lastClickRow, m.lastClickAt = i, time.Now()
				ov.focus = i
				if double {
					m.lastClickRow = -1
					return m.overviewJump()
				}
			}
		}
	}
	return false, nil, nil
}

// move moves the focus in direction (dx, dy). The focus point keeps its
// column when moving up or down, and its row when moving left or right,
// within the window it lands on.
func (ov *overview) move(dx, dy int) {
	if ov.focus >= len(ov.cells) {
		return
	}
	ov.px, ov.py = ov.point()
	ov.focus = ov.nearest(dx, dy)
	b := ov.cells[ov.focus]
	if dx != 0 {
		ov.px, ov.py = (b.x0+b.x1)/2, max(b.y0, min(ov.py, b.y1))
	} else {
		ov.px, ov.py = max(b.x0, min(ov.px, b.x1)), (b.y0+b.y1)/2
	}
}

// point is the focus point, or the middle of the focused window if the
// point is not in it, as after the focus moved some other way.
func (ov *overview) point() (int, int) {
	f := ov.cells[ov.focus]
	if f.contains(ov.px, ov.py) {
		return ov.px, ov.py
	}
	return (f.x0 + f.x1) / 2, (f.y0 + f.y1) / 2
}

// nearest is the window to move the focus to in direction (dx, dy). Of the
// windows beyond the focused one that way, it prefers one in line with the
// focus point (spanning its column, or row), then one beside the focused
// window, then the closest, then the best aligned with the focus point.
// Comparing boxes rather than centre points keeps the large centre window
// reachable from all around it. With nothing that way, the focus wraps
// around.
func (ov *overview) nearest(dx, dy int) int {
	if ov.focus >= len(ov.cells) {
		return ov.focus
	}
	f := ov.cells[ov.focus]
	best := ov.focus
	var bestRank [4]int
	for i, b := range ov.cells {
		if i == ov.focus {
			continue
		}
		gap := map[[2]int]int{{1, 0}: b.x0 - f.x1, {-1, 0}: f.x0 - b.x1, {0, 1}: b.y0 - f.y1, {0, -1}: f.y0 - b.y1}[[2]int{dx, dy}]
		if gap <= 0 {
			continue // not beyond the focused box that way
		}
		if rank := ov.alignment(b, dx); best == ov.focus || less4(rank(gap), bestRank) {
			best, bestRank = i, rank(gap)
		}
	}
	if best == ov.focus {
		return ov.wrap(dx, dy)
	}
	return best
}

// alignment ranks box b, for a move along x if dx != 0 or else along y, by
// how it lines up with the focus point and the focused box; the returned
// function completes the rank with a distance.
func (ov *overview) alignment(b cellBox, dx int) func(distance int) [4]int {
	f := ov.cells[ov.focus]
	px, py := ov.point()
	lo, hi, flo, fhi, p := b.x0, b.x1, f.x0, f.x1, px
	if dx != 0 {
		lo, hi, flo, fhi, p = b.y0, b.y1, f.y0, f.y1, py
	}
	inLine, beside := 1, 1
	if lo <= p && p <= hi {
		inLine = 0
	}
	if min(hi, fhi) >= max(lo, flo) {
		beside = 0
	}
	offset := abs(lo + hi - 2*p)
	return func(distance int) [4]int { return [4]int{inLine, beside, distance, offset} }
}

// wrap is the window to focus when nothing is beyond the focused one in
// direction (dx, dy): the farthest one the other way, preferring those in
// line with the focus point, as if the screen wrapped around.
func (ov *overview) wrap(dx, dy int) int {
	best := ov.focus
	var bestRank [4]int
	for i, b := range ov.cells {
		if i == ov.focus {
			continue
		}
		rank := ov.alignment(b, dx)
		if r := rank(0); r[0] == 1 && r[1] == 1 {
			continue // neither in line nor beside: not across the wrap
		}
		far := dx*b.x0 + dy*b.y0 // the farther the other way, the smaller
		if r := rank(far); best == ov.focus || less4(r, bestRank) {
			best, bestRank = i, r
		}
	}
	return best
}

func less4(a, b [4]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// overviewLayout places windows in a w×h area: around windows[center] if
// center >= 0, else evenly. It returns a box per window, in order. sizes
// (nil for all 1) make windows larger or smaller than their share; see
// centredLayout and evenLayout.
func overviewLayout(windows []*tmux.Window, center, w, h int, sizes windowSizes) []cellBox {
	if center < 0 {
		return evenLayout(windows, w, h, sizes)
	}
	return centredLayout(windows, center, w, h, sizes)
}

// centredLayout puts windows[center] in the middle of a k×k grid of cells,
// taking its middle half each way, and the other windows in the ring of
// cells around it, the layer next to the centre first, each layer clockwise
// from its top left corner. A grown window takes more ring cells while there
// are empty ones (see packCells); the rest of its size, and the centre's
// middle size, scale the grid's columns and rows (see trackWeights).
func centredLayout(windows []*tmux.Window, center, w, h int, sizes windowSizes) []cellBox {
	n := len(windows)
	k := overviewRing
	for k*k-(k/2)*(k/2) < n-1 {
		k += overviewRing
	}
	lo, hi := k/4, 3*k/4-1 // the centre's first and last cell each way

	type cell struct {
		c, r  int
		layer int
		angle float64
	}
	var ring []cell
	free := make([][]bool, k)
	mid := float64(k) / 2
	for r := 0; r < k; r++ {
		free[r] = make([]bool, k)
		for c := 0; c < k; c++ {
			layer := max(lo-c, c-hi, lo-r, r-hi)
			if layer <= 0 {
				continue // the centre
			}
			free[r][c] = true
			dx, dy := float64(c)+0.5-mid, float64(r)+0.5-mid
			angle := math.Atan2(dy, dx) + 3*math.Pi/4 // the top left corner first
			if angle < 0 {
				angle += 2 * math.Pi
			}
			ring = append(ring, cell{c, r, layer, angle})
		}
	}
	sort.SliceStable(ring, func(a, b int) bool {
		if ring[a].layer != ring[b].layer {
			return ring[a].layer < ring[b].layer
		}
		return ring[a].angle < ring[b].angle
	})
	order := make([][2]int, len(ring))
	for i, c := range ring {
		order[i] = [2]int{c.c, c.r}
	}

	var others []*tmux.Window
	for i, win := range windows {
		if i != center {
			others = append(others, win)
		}
	}
	rects, factors := packWindows(others, free, order, float64(w)/float64(k), float64(h)/float64(k), sizes)
	colW, rowH := trackWeights(rects, factors, k, k, nil)
	middle := sizes.of(windows[center], true)
	for x := lo; x <= hi; x++ {
		colW[x], rowH[x] = middle, middle
	}
	xs, ys := cuts(w, colW), cuts(h, rowH)
	boxes := make([]cellBox, n)
	boxes[center] = cellBox{xs[lo], ys[lo], xs[hi+1] - 1, ys[hi+1] - 1}
	j := 0
	for i := range windows {
		if i != center {
			r := rects[j]
			boxes[i] = cellBox{xs[r.c0], ys[r.r0], xs[r.c1+1] - 1, ys[r.r1+1] - 1}
			j++
		}
	}
	return boxes
}

// evenLayout shares a w×h area among windows in a grid, choosing the number
// of columns that shows the windows largest, and filling it row by row. A
// grown window takes more cells while the grid has empty ones (see
// packCells); the rest of its size scales the grid's columns and rows (see
// trackWeights). A row left with empty cells, whose windows are all within
// it, spreads them across the full width instead.
func evenLayout(windows []*tmux.Window, w, h int, sizes windowSizes) []cellBox {
	n := len(windows)
	ww, wh := 80, 24
	if n > 0 && windows[0].Width > 0 && windows[0].Height > 0 {
		ww, wh = windows[0].Width, windows[0].Height
	}
	cols, best := 1, -1.0
	for c := 1; c <= n; c++ {
		r := (n + c - 1) / c
		scale := math.Min(float64(w/c-2)/float64(ww), float64(h/r-2)/float64(wh))
		if scale > best {
			cols, best = c, scale
		}
	}
	rows := (n + cols - 1) / cols
	free := make([][]bool, rows)
	var order [][2]int
	for r := range free {
		free[r] = make([]bool, cols)
		for c := range free[r] {
			free[r][c] = true
			order = append(order, [2]int{c, r})
		}
	}
	rects, factors := packWindows(windows, free, order, float64(w)/float64(cols), float64(h)/float64(rows), sizes)

	// Rows to spread: those with empty cells and no window reaching into
	// another row.
	used := make([]int, rows)
	spread := make([]bool, rows)
	for r := range spread {
		spread[r] = true
	}
	for _, rc := range rects {
		for r := rc.r0; r <= rc.r1; r++ {
			used[r] += rc.cols()
			if rc.r0 != rc.r1 {
				spread[r] = false
			}
		}
	}
	for r := range spread {
		spread[r] = spread[r] && used[r] < cols
	}
	colW, rowH := trackWeights(rects, factors, cols, rows, spread)
	xs, ys := cuts(w, colW), cuts(h, rowH)
	boxes := make([]cellBox, n)
	for r := 0; r < rows; r++ {
		if !spread[r] {
			continue
		}
		var in []int
		for i, rc := range rects {
			if rc.r0 == r {
				in = append(in, i)
			}
		}
		sort.SliceStable(in, func(a, b int) bool { return rects[in[a]].c0 < rects[in[b]].c0 })
		widths := make([]float64, len(in))
		for j, i := range in {
			widths[j] = float64(rects[i].cols()) * factors[i]
		}
		rowXs := cuts(w, widths)
		for j, i := range in {
			boxes[i] = cellBox{rowXs[j], ys[r], rowXs[j+1] - 1, ys[r+1] - 1}
		}
	}
	for i, rc := range rects {
		if !spread[rc.r0] {
			boxes[i] = cellBox{xs[rc.c0], ys[rc.r0], xs[rc.c1+1] - 1, ys[rc.r1+1] - 1}
		}
	}
	return boxes
}

// drawOverview draws the overview over the whole screen: a header, the
// windows, and a footer with its keys.
func (m *model) drawOverview(b *strings.Builder, width, height int) {
	ov := m.overview
	s := ov.session
	kind := "all windows"
	if ov.center != nil {
		kind = fmt.Sprintf("around %d: %s", ov.center.Index, clean(ov.center.Name))
	}
	count := fmt.Sprintf("%d windows", len(s.Windows))
	if len(m.tags) > 0 {
		count = fmt.Sprintf("%d tagged  ", len(m.tags)) + count
	}
	right := span{text: count, style: styleDim}
	if m.confirm != nil {
		right = span{text: clean(m.confirm.question) + " (y/n)", style: styleConfirm}
	}
	if m.status != "" {
		right = span{text: clean(m.status), style: styleError}
	}
	left := []span{{text: " OVERVIEW ", style: styleNormal}, {text: " " + clean(s.Name), style: styleHeading},
		{text: "  " + kind, style: styleDim}}
	if ov.typing != nil {
		left, right = m.typingHeader(), span{}
	} else if m.input != nil {
		left = append(left[:2], span{text: "  " + clean(m.input.label), style: styleConfirm},
			span{text: clean(string(m.input.text))})
		right = span{text: "Enter run  Esc cancel", style: styleDim}
	} else if ov.search != nil {
		var prompt []span
		prompt, right = m.searchHeader()
		left = append(left[:2], prompt...)
	} else if ov.focus < len(s.Windows) {
		focused := s.Windows[ov.focus]
		if size := m.sizes.of(focused, focused == ov.center); size != 1 {
			left = append(left, span{text: fmt.Sprintf("  size %.3g×", size), style: styleDim})
		}
	}
	used := 0
	for _, sp := range left {
		used += textWidth(sp.text)
	}
	gap := span{text: strings.Repeat(" ", max(2, width-used-textWidth(right.text)))}
	drawLine(b, 0, width, "", false, append(left, gap, right)...)
	cursor := 0 // the column after the query or command being typed
	if m.input != nil || ov.search != nil {
		cursor = 1 + used
	}

	area := max(1, height-2)
	c := newCanvas(width, area)
	typeX, typeY := 0, 0 // the cursor of the pane typed into
	center := -1
	for i, w := range s.Windows {
		if w == ov.center {
			center = i
		}
	}
	ov.cells = overviewLayout(s.Windows, center, width, area, m.sizes)
	ov.width, ov.height = width, area
	var panes []*tmux.Pane
	for _, w := range s.Windows {
		panes = append(panes, layoutPanes(w)...)
	}
	m.loadScreens(panes)
	for i, w := range s.Windows {
		box := ov.cells[i]
		if box.x1-box.x0 < 2 || box.y1-box.y0 < 2 {
			continue
		}
		m.drawWindow(c, s.ID, w, box.x0, box.y0, box.x1, box.y1, "", false)
		if ov.search.active() {
			for _, r := range paneRects(w, box.x0, box.y0, box.x1, box.y1) {
				if !m.paneMatches(w, r.p) {
					c.dimPane(r.x0, r.y0, r.x1, r.y1)
				}
			}
		}
		if m.dirs {
			m.drawDirs(c, w, box.x0, box.y0, box.x1, box.y1)
		}
		label, style := fmt.Sprintf(" %d: %s%s ", w.Index, clean(w.Name), clean(w.Flags)), styleLabel
		if ov.search.active() && !m.windowMatches(w) {
			style = styleDim
		}
		if m.windowTagged(w) {
			c.restyleBox(box.x0, box.y0, box.x1, box.y1, styleTagged)
			label, style = " ✓"+label, styleTagged
		}
		if i == ov.focus {
			c.restyleBox(box.x0, box.y0, box.x1, box.y1, styleFocus)
			label, style = " ▶"+label, styleFocusLabel
		}
		c.text(box.x0+2, box.y0, box.x1-1, label, style)
		if m.titles {
			m.drawPaneTitles(c, w, box.x0, box.y0, box.x1, box.y1, box.x0+2+textWidth(label))
		}
		if ov.typing != nil && w.ID == ov.typing.window {
			typeX, typeY = m.drawTyping(c, w, box)
		}
	}
	// Screen rows of the boxes, for the mouse.
	for i := range ov.cells {
		ov.cells[i].y0++
		ov.cells[i].y1++
	}
	c.draw(b, 1)
	leave := "back"
	if m.overviewOnly {
		leave = "close"
	}
	// The keys to leave first, so a narrow screen cuts the others.
	foot := " ←↑↓→ hjkl move  Enter switch  Esc/q " + leave + "  i type  Tab next  0-9 window  / search  t tag  x kill  : command  d dirs  o centre  + - = size  == all  w live window  ^L titles"
	if ov.search != nil {
		foot = " type to search  Tab Shift-Tab ←↑↓→ next / previous match  ^W ^U delete a word / all  Esc Enter done"
	}
	if ov.typing != nil {
		drawLine(b, height-1, width, "", false, m.typingFooter()...)
	} else {
		drawLine(b, height-1, width, "", false, hintSpans(foot, m.togglesOn())...)
	}
	switch {
	case cursor > 0:
		fmt.Fprintf(b, "\x1b[1;%dH\x1b[?25h", min(width, cursor))
	case typeX > 0:
		fmt.Fprintf(b, "\x1b[%d;%dH\x1b[?25h", typeY, typeX)
	}
}

// restyleBox gives the border of a box a style, keeping its characters.
func (c *canvas) restyleBox(x0, y0, x1, y1 int, style string) {
	for x := x0; x <= x1; x++ {
		for _, y := range []int{y0, y1} {
			if x >= 0 && y >= 0 && x < c.w && y < c.h {
				c.cells[y][x].style = style
			}
		}
	}
	for y := y0; y <= y1; y++ {
		for _, x := range []int{x0, x1} {
			if x >= 0 && y >= 0 && x < c.w && y < c.h {
				c.cells[y][x].style = style
			}
		}
	}
}

// drawPaneTitles writes what each pane of w is working on (see paneTitle)
// on the top border of its pane, in the window's box (x0, y0)–(x1, y1).
// Panes along the box's top share that border with the window's label,
// which ends at column after, so their titles go after it.
func (m *model) drawPaneTitles(c *canvas, w *tmux.Window, x0, y0, x1, y1, after int) {
	rects := paneRects(w, x0, y0, x1, y1)
	// Left to right, which pane indexes need not follow.
	sort.Slice(rects, func(i, j int) bool { return rects[i].x0 < rects[j].x0 })
	for _, r := range rects {
		title, style := m.paneTitle(r.p)
		if title == "" {
			continue
		}
		x := r.x0 + 2
		if r.y0 == y0 {
			x = max(x, after+1)
		}
		text := " " + title + " "
		c.text(x, r.y0, r.x1-1, text, style)
		if r.y0 == y0 && x < r.x1-1 {
			after = min(x+textWidth(text), r.x1-1)
		}
	}
}

// drawDirs writes each pane's working directory at the right of its bottom
// border, in the window's box (x0, y0)–(x1, y1). A path too long for the
// border keeps its end, the most telling part, after a "…".
func (m *model) drawDirs(c *canvas, w *tmux.Window, x0, y0, x1, y1 int) {
	for _, r := range paneRects(w, x0, y0, x1, y1) {
		room := r.x1 - r.x0 - 5 // corners, a column of border each side, spaces
		path := []rune(clean(tilde(r.p.Path, m.home)))
		if room < 4 || len(path) == 0 {
			continue
		}
		for textWidth(string(path)) > room {
			path = path[1:]
			if textWidth("…"+string(path)) <= room {
				path = append([]rune("…"), path...)
				break
			}
		}
		text := " " + string(path) + " "
		c.text(r.x1-1-textWidth(text), r.y1, r.x1, text, stylePath)
	}
}
