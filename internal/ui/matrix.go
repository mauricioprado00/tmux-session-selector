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
	// matrixRing is the smallest grid, in cells per side, of a centred matrix:
	// the window in focus takes its middle half, the others the ring around.
	matrixRing = 4
)

// matrixView shows every window of a session at once, live, for watching
// them. With a centre window, that window takes the middle half of the
// screen each way and the others surround it; otherwise all windows share
// the screen evenly.
type matrixView struct {
	session *tmux.Session
	center  *tmux.Window // nil for an even grid
	focus   int          // index into session.Windows
	cells   []cellBox    // where each window was drawn, by index into session.Windows
}

// cellBox is a box on screen, corners inclusive.
type cellBox struct {
	x0, y0, x1, y1 int
}

func (b cellBox) contains(x, y int) bool {
	return x >= b.x0 && x <= b.x1 && y >= b.y0 && y <= b.y1
}

// openMatrix opens the matrix view of the selected row's session: centred on
// the selected window, or even for a session row.
func (m *model) openMatrix() {
	n := m.selected()
	if n == nil || len(n.session.Windows) == 0 {
		return
	}
	mv := &matrixView{session: n.session}
	focus := n.session.ActiveWindow()
	if n.kind != sessionRow {
		mv.center, focus = n.window, n.window
	}
	for i, w := range n.session.Windows {
		if w == focus {
			mv.focus = i
		}
	}
	m.matrix = mv
}

// matrixKey handles an event while the matrix view is open.
func (m *model) matrixKey(ev event) (done bool, target *tmux.Target, err error) {
	mv := m.matrix
	dirs := map[key][2]int{keyUp: {0, -1}, keyDown: {0, 1}, keyExpand: {1, 0}, keyCollapse: {-1, 0}}
	letters := map[rune][2]int{'k': {0, -1}, 'j': {0, 1}, 'l': {1, 0}, 'h': {-1, 0}}
	if d, ok := dirs[ev.key]; ok {
		mv.focus = mv.nearest(d[0], d[1])
		return false, nil, nil
	}
	switch ev.key {
	case keyRune:
		switch d, ok := letters[ev.r]; {
		case ok:
			mv.focus = mv.nearest(d[0], d[1])
		case ev.r == 'o':
			mv.toggleCenter()
		case ev.r == 'q':
			m.matrix = nil
		}
	case keyMatrix:
		mv.toggleCenter()
	case keyCancel:
		m.matrix = nil
	case keyEnter:
		return m.matrixJump()
	case keyMouse:
		return m.matrixMouse(ev)
	}
	return false, nil, nil
}

// toggleCenter puts the window in focus in the centre, or, if it is there
// already, goes back to sharing the screen evenly.
func (mv *matrixView) toggleCenter() {
	focused := mv.session.Windows[mv.focus]
	if mv.center == focused {
		mv.center = nil
	} else {
		mv.center = focused
	}
}

// matrixJump switches to the window in focus, as choosing it in the list
// would. On failure the matrix closes so the error shows on the list.
func (m *model) matrixJump() (done bool, target *tmux.Target, err error) {
	mv := m.matrix
	w := mv.session.Windows[mv.focus]
	done, target, err = m.jump(tmux.Target{Session: mv.session.ID, Window: w.ID})
	if !done {
		m.matrix = nil
	}
	return done, target, err
}

// matrixMouse focuses the window clicked, or switches to it on a double
// click; the wheel moves the focus through the windows in order.
func (m *model) matrixMouse(ev event) (done bool, target *tmux.Target, err error) {
	mv := m.matrix
	button := ev.button &^ (4 | 8 | 16)
	n := len(mv.session.Windows)
	switch {
	case !ev.press:
	case button == 64:
		mv.focus = (mv.focus + n - 1) % n
	case button == 65:
		mv.focus = (mv.focus + 1) % n
	case button == 0:
		for i, c := range mv.cells {
			if c.contains(ev.x, ev.y) {
				double := i == m.lastClickRow && time.Since(m.lastClickAt) < doubleClick
				m.lastClickRow, m.lastClickAt = i, time.Now()
				mv.focus = i
				if double {
					m.lastClickRow = -1
					return m.matrixJump()
				}
			}
		}
	}
	return false, nil, nil
}

// nearest is the window whose box is nearest the focused one in direction
// (dx, dy), or the focused one if there is none that way. Rows count double,
// as terminal cells are about twice as tall as wide.
func (mv *matrixView) nearest(dx, dy int) int {
	if mv.focus >= len(mv.cells) {
		return mv.focus
	}
	center := func(b cellBox) (int, int) { return b.x0 + b.x1, 2 * (b.y0 + b.y1) }
	fx, fy := center(mv.cells[mv.focus])
	best, bestScore := mv.focus, math.MaxInt
	for i, b := range mv.cells {
		if i == mv.focus {
			continue
		}
		cx, cy := center(b)
		along := (cx-fx)*dx + (cy-fy)*dy
		across := abs((cx-fx)*dy) + abs((cy-fy)*dx)
		if along <= 0 {
			continue
		}
		if score := along + 2*across; score < bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// matrixLayout places windows in a w×h area: around windows[center] if
// center >= 0, else evenly. It returns a box per window, in order.
func matrixLayout(windows []*tmux.Window, center, w, h int) []cellBox {
	if center < 0 {
		return evenLayout(windows, w, h)
	}
	n := len(windows)
	k := matrixRing
	for k*k-(k/2)*(k/2) < n-1 {
		k += matrixRing
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

// drawMatrix draws the matrix view over the whole screen: a header, the
// windows, and a footer with its keys.
func (m *model) drawMatrix(b *strings.Builder, width, height int) {
	mv := m.matrix
	s := mv.session
	kind := "all windows"
	if mv.center != nil {
		kind = fmt.Sprintf("around %d: %s", mv.center.Index, clean(mv.center.Name))
	}
	right := span{text: fmt.Sprintf("%d windows", len(s.Windows)), style: styleDim}
	if m.status != "" {
		right = span{text: clean(m.status), style: styleError}
	}
	left := []span{{text: " MATRIX ", style: styleNormal}, {text: " " + clean(s.Name), style: styleHeading},
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
		if w == mv.center {
			center = i
		}
	}
	mv.cells = matrixLayout(s.Windows, center, width, area)
	var panes []*tmux.Pane
	for _, w := range s.Windows {
		panes = append(panes, layoutPanes(w)...)
	}
	m.loadScreens(panes)
	for i, w := range s.Windows {
		box := mv.cells[i]
		if box.x1-box.x0 < 2 || box.y1-box.y0 < 2 {
			continue
		}
		m.drawWindow(c, s.ID, w, box.x0, box.y0, box.x1, box.y1, "", false)
		label, style := fmt.Sprintf(" %d: %s%s ", w.Index, clean(w.Name), clean(w.Flags)), styleLabel
		if i == mv.focus {
			c.restyleBox(box.x0, box.y0, box.x1, box.y1, styleFocus)
			label, style = " ▶"+label, styleFocusLabel
		}
		c.text(box.x0+2, box.y0, box.x1-1, label, style)
	}
	// Screen rows of the boxes, for the mouse.
	for i := range mv.cells {
		mv.cells[i].y0++
		mv.cells[i].y1++
	}
	c.draw(b, 1)
	drawLine(b, height-1, width, styleDim, false,
		span{text: " ←↑↓→ or hjkl focus  o centre / uncentre  Enter switch to it  double-click switch  Esc/q back"})
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
