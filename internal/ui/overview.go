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
		case ev.r == 'o':
			ov.toggleCenter()
		case ev.r == 'q':
			return m.leaveOverview()
		}
	case keyOverview:
		ov.toggleCenter()
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
	sessions, _ = withoutPane(sessions, m.self, tmux.Location{})
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
// center >= 0, else evenly. It returns a box per window, in order.
func overviewLayout(windows []*tmux.Window, center, w, h int) []cellBox {
	if center < 0 {
		return evenLayout(windows, w, h)
	}
	n := len(windows)
	k := overviewRing
	for k*k-(k/2)*(k/2) < n-1 {
		k += overviewRing
	}
	col := func(i int) int { return i * w / k }
	row := func(j int) int { return j * h / k }
	boxes := make([]cellBox, n)
	boxes[center] = cellBox{col(k / 4), row(k / 4), col(3*k/4) - 1, row(3*k/4) - 1}

	// The ring's cells: the layer next to the centre first, each layer
	// clockwise from its top left corner.
	type cell struct {
		box   cellBox
		layer int
		angle float64
	}
	var ring []cell
	lo, hi := k/4, 3*k/4-1 // the centre's first and last cell each way
	mid := float64(k) / 2
	for j := 0; j < k; j++ {
		for i := 0; i < k; i++ {
			layer := max(lo-i, i-hi, lo-j, j-hi)
			if layer <= 0 {
				continue // the centre
			}
			dx, dy := float64(i)+0.5-mid, float64(j)+0.5-mid
			angle := math.Atan2(dy, dx) + 3*math.Pi/4 // the top left corner first
			if angle < 0 {
				angle += 2 * math.Pi
			}
			ring = append(ring, cell{cellBox{col(i), row(j), col(i+1) - 1, row(j+1) - 1}, layer, angle})
		}
	}
	sort.SliceStable(ring, func(a, b int) bool {
		if ring[a].layer != ring[b].layer {
			return ring[a].layer < ring[b].layer
		}
		return ring[a].angle < ring[b].angle
	})
	next := 0
	for i := range windows {
		if i != center {
			boxes[i] = ring[next].box
			next++
		}
	}
	return boxes
}

// evenLayout shares a w×h area among windows in rows, choosing the number
// of columns that shows the windows largest; a last, shorter row is spread
// across the full width.
func evenLayout(windows []*tmux.Window, w, h int) []cellBox {
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
	boxes := make([]cellBox, n)
	for i := range windows {
		r, c := i/cols, i%cols
		inRow := cols
		if r == rows-1 {
			inRow = n - r*cols
		}
		boxes[i] = cellBox{c * w / inRow, r * h / rows, (c+1)*w/inRow - 1, (r+1)*h/rows - 1}
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
	right := span{text: fmt.Sprintf("%d windows", len(s.Windows)), style: styleDim}
	if m.confirm != nil {
		right = span{text: clean(m.confirm.question) + " (y/n)", style: styleConfirm}
	}
	if m.status != "" {
		right = span{text: clean(m.status), style: styleError}
	}
	left := []span{{text: " OVERVIEW ", style: styleNormal}, {text: " " + clean(s.Name), style: styleHeading},
		{text: "  " + kind, style: styleDim}}
	used := 0
	for _, sp := range left {
		used += textWidth(sp.text)
	}
	gap := span{text: strings.Repeat(" ", max(2, width-used-textWidth(right.text)))}
	drawLine(b, 0, width, "", false, append(left, gap, right)...)

	area := max(1, height-2)
	c := newCanvas(width, area)
	center := -1
	for i, w := range s.Windows {
		if w == ov.center {
			center = i
		}
	}
	ov.cells = overviewLayout(s.Windows, center, width, area)
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
		label, style := fmt.Sprintf(" %d: %s%s ", w.Index, clean(w.Name), clean(w.Flags)), styleLabel
		if i == ov.focus {
			c.restyleBox(box.x0, box.y0, box.x1, box.y1, styleFocus)
			label, style = " ▶"+label, styleFocusLabel
		}
		c.text(box.x0+2, box.y0, box.x1-1, label, style)
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
	drawLine(b, height-1, width, styleDim, false,
		span{text: " ←↑↓→ hjkl move  Tab next  0-9 window number  o centre  w make a live window  Enter switch  Esc/q " + leave})
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
