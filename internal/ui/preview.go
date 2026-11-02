package ui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

const (
	styleBorder       = ""
	styleActiveBorder = "32"
	styleLabel        = "1"
	styleActiveLabel  = "1;32"
	// minWindowBox is the narrowest window box in a session preview, the same
	// as in tmux's chooser.
	minWindowBox = 24
	// arrowBox is the width of the scroll arrows of a session preview.
	arrowBox = 3
)

// cell is one terminal cell. The second column of a wide character is a cell
// with r == 0.
type cell struct {
	r     rune
	style string
}

// canvas is a grid of cells that is drawn to the screen in one go.
type canvas struct {
	w, h  int
	cells [][]cell
	lines [][]uint8 // box-drawing directions at each cell
}

const (
	lineUp uint8 = 1 << iota
	lineDown
	lineLeft
	lineRight
)

// boxChars is indexed by a combination of line directions.
var boxChars = [16]rune{
	' ', '│', '│', '│',
	'─', '┘', '┐', '┤',
	'─', '└', '┌', '├',
	'─', '┴', '┬', '┼',
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h, cells: make([][]cell, h), lines: make([][]uint8, h)}
	for y := range c.cells {
		c.cells[y] = make([]cell, w)
		c.lines[y] = make([]uint8, w)
		for x := range c.cells[y] {
			c.cells[y][x] = cell{r: ' '}
		}
	}
	return c
}

func (c *canvas) set(x, y int, r rune, style string) {
	if x >= 0 && y >= 0 && x < c.w && y < c.h {
		c.cells[y][x] = cell{r: r, style: style}
	}
}

// text writes s from column x of row y, stopping before column limit.
func (c *canvas) text(x, y, limit int, s, style string) {
	for _, r := range s {
		w := runeWidth(r)
		if w == 0 {
			continue
		}
		if x+w > limit {
			return
		}
		c.set(x, y, r, style)
		if w == 2 {
			c.set(x+1, y, 0, style)
		}
		x += w
	}
}

// line adds directions to the box-drawing character at (x, y), so boxes that
// share an edge meet with the right junctions.
func (c *canvas) line(x, y int, dirs uint8, style string) {
	if x >= 0 && y >= 0 && x < c.w && y < c.h {
		c.lines[y][x] |= dirs
		c.cells[y][x] = cell{r: boxChars[c.lines[y][x]], style: style}
	}
}

// box draws a rectangle with corners (x0, y0) and (x1, y1).
func (c *canvas) box(x0, y0, x1, y1 int, style string) {
	if x1 <= x0 || y1 <= y0 {
		return
	}
	for x := x0; x <= x1; x++ {
		dirs := lineLeft | lineRight
		if x == x0 {
			dirs = lineRight
		} else if x == x1 {
			dirs = lineLeft
		}
		c.line(x, y0, dirs, style)
		c.line(x, y1, dirs, style)
	}
	for y := y0; y <= y1; y++ {
		dirs := lineUp | lineDown
		if y == y0 {
			dirs = lineDown
		} else if y == y1 {
			dirs = lineUp
		}
		c.line(x0, y, dirs, style)
		c.line(x1, y, dirs, style)
	}
}

// blit copies a pane's screen into the w×h area at (x, y). Like tmux's own
// preview, it shows the top-left of the pane unless that would hide the
// cursor, in which case it scrolls just enough to show it.
func (c *canvas) blit(x, y, w, h int, screen [][]cell, cx, cy int) {
	ox, oy := 0, 0
	if cx >= w {
		ox = cx - w + 1
	}
	if cy >= h {
		oy = cy - h + 1
	}
	for row := 0; row < h && oy+row < len(screen); row++ {
		line := screen[oy+row]
		for col := 0; col < w && ox+col < len(line); col++ {
			cl := line[ox+col]
			c.set(x+col, y+row, cl.r, cl.style)
		}
	}
}

// draw writes the canvas to the screen starting at screen row y0 (0-based).
func (c *canvas) draw(b *strings.Builder, y0 int) {
	for y, row := range c.cells {
		fmt.Fprintf(b, "\x1b[%d;1H\x1b[0m", y0+y+1)
		cur := ""
		for x := 0; x < len(row); x++ {
			cl := row[x]
			r := cl.r
			wide := r != 0 && runeWidth(r) == 2
			switch {
			case r == 0:
				r = ' ' // second half of a wide character that was cut off
			case wide && (x+1 >= len(row) || row[x+1].r != 0):
				r = ' ' // wide character whose second half was overwritten
				wide = false
			}
			if cl.style != cur {
				b.WriteString("\x1b[0;" + cl.style + "m")
				cur = cl.style
			}
			b.WriteRune(r)
			if wide {
				x++
			}
		}
		b.WriteString("\x1b[0m")
	}
}

// parseScreen turns capture-pane -e output into rows of cells.
func parseScreen(s string) [][]cell {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	screen := make([][]cell, len(lines))
	var st sgr
	for i, line := range lines {
		var row []cell
		for j := 0; j < len(line); {
			if line[j] == 0x1b {
				j += st.escape(line[j:])
				continue
			}
			r, n := utf8.DecodeRuneInString(line[j:])
			j += n
			if r == '\t' || isControl(r) {
				r = ' '
			}
			w := runeWidth(r)
			if w == 0 {
				continue
			}
			style := st.String()
			row = append(row, cell{r: r, style: style})
			if w == 2 {
				row = append(row, cell{r: 0, style: style})
			}
		}
		screen[i] = row
	}
	return screen
}

// sgr tracks text attributes and colours across SGR escape sequences, so each
// cell can carry a short, complete style.
type sgr struct {
	attrs  [10]bool // SGR 1–9: bold, dim, italic, underline, blink, …, reverse, hidden, strike
	fg, bg string
	cached string
	valid  bool
}

// escape applies the escape sequence at the start of s and returns its
// length. Sequences other than SGR are skipped.
func (st *sgr) escape(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				if s[i] == 'm' {
					st.apply(s[2:i])
				}
				return i + 1
			}
		}
		return len(s)
	case ']': // OSC, such as a hyperlink, ended by BEL or ESC \
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	}
	return 2
}

func (st *sgr) apply(params string) {
	st.valid = false
	ps := strings.Split(params, ";")
	for i := 0; i < len(ps); i++ {
		p := ps[i]
		if head, _, ok := strings.Cut(p, ":"); ok {
			// Colon sub-parameters: 38:5:n, 38:2::r:g:b, 4:3 (curly underline).
			switch head {
			case "38":
				st.fg = p
			case "48":
				st.bg = p
			case "4":
				st.attrs[4] = !strings.HasSuffix(p, ":0")
			}
			continue
		}
		n, err := strconv.Atoi(p)
		if p == "" {
			n, err = 0, nil
		}
		if err != nil {
			continue
		}
		switch {
		case n == 0:
			*st = sgr{}
		case n >= 1 && n <= 9:
			st.attrs[n] = true
		case n == 22:
			st.attrs[1], st.attrs[2] = false, false
		case n >= 23 && n <= 29:
			st.attrs[n-20] = false
		case n == 39:
			st.fg = ""
		case n == 49:
			st.bg = ""
		case n == 38 || n == 48:
			var color string
			switch {
			case i+2 < len(ps) && ps[i+1] == "5":
				color = strings.Join(ps[i:i+3], ";")
				i += 2
			case i+4 < len(ps) && ps[i+1] == "2":
				color = strings.Join(ps[i:i+5], ";")
				i += 4
			default:
				// Incomplete colour: drop the rest rather than misread it.
				i = len(ps)
				continue
			}
			if n == 38 {
				st.fg = color
			} else {
				st.bg = color
			}
		case n >= 30 && n <= 37, n >= 90 && n <= 97:
			st.fg = p
		case n >= 40 && n <= 47, n >= 100 && n <= 107:
			st.bg = p
		}
	}
}

func (st *sgr) String() string {
	if !st.valid {
		var parts []string
		for i, on := range st.attrs {
			if on {
				parts = append(parts, strconv.Itoa(i))
			}
		}
		if st.fg != "" {
			parts = append(parts, st.fg)
		}
		if st.bg != "" {
			parts = append(parts, st.bg)
		}
		st.cached = strings.Join(parts, ";")
		st.valid = true
	}
	return st.cached
}

// screen is a pane's captured content, or why it could not be captured.
type screen struct {
	cells [][]cell
	err   error
}

// previewPanes lists the panes the preview of n shows in width columns.
func (m *model) previewPanes(n *node, width int) []*tmux.Pane {
	switch n.kind {
	case paneRow, jobRow:
		return []*tmux.Pane{n.pane}
	case windowRow:
		return layoutPanes(n.window)
	}
	ws, _, _ := m.sessionView(n.session, width)
	var panes []*tmux.Pane
	for _, w := range ws {
		panes = append(panes, layoutPanes(w)...)
	}
	return panes
}

// layoutPanes is the panes a window shows: only the active one when zoomed.
func layoutPanes(w *tmux.Window) []*tmux.Pane {
	if w.Zoomed {
		if p := w.ActivePane(); p != nil {
			return []*tmux.Pane{p}
		}
	}
	return w.Panes
}

// sessionView picks the windows of s that fit side by side in width
// columns, starting at the scroll offset, and says whether more are hidden to
// the left or right. A newly shown session starts with its active window in
// the middle.
func (m *model) sessionView(s *tmux.Session, width int) (ws []*tmux.Window, left, right bool) {
	// Counted as tmux does: the arrows then take their columns from the boxes.
	n := len(s.Windows)
	if n == 0 || width/n >= minWindowBox {
		m.winOffset, m.winOffsetFor = 0, s
		return s.Windows, false, false
	}
	visible := max(1, width/minWindowBox)
	if m.winOffsetFor != s {
		m.winOffsetFor = s
		m.winOffset = 0
		for i, w := range s.Windows {
			if w.Active {
				m.winOffset = i - visible/2
			}
		}
	}
	m.winOffset = max(0, min(m.winOffset, n-visible))
	return s.Windows[m.winOffset : m.winOffset+visible], m.winOffset > 0, m.winOffset+visible < n
}

// loadScreens captures the panes not yet in the cache, in one tmux call.
// The cache is emptied on every refresh tick, which makes the preview live.
func (m *model) loadScreens(panes []*tmux.Pane) {
	var ids []string
	for _, p := range panes {
		if _, ok := m.screens[p.ID]; !ok {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	out, err := m.tmux.Capture(ids...)
	if err != nil && len(ids) > 1 {
		// A pane may have closed since the tree was read, which fails the
		// whole batch: capture the panes one by one, so only that one is lost.
		out = map[string]tmux.Screen{}
		for _, id := range ids {
			if one, err := m.tmux.Capture(id); err != nil {
				m.screens[id] = screen{err: err}
			} else {
				out[id] = one[id]
			}
		}
		err = nil
	}
	for _, id := range ids {
		switch sc, ok := out[id]; {
		case err != nil:
			m.screens[id] = screen{err: err}
		case ok:
			m.screens[id] = screen{cells: parseScreen(sc.Text)}
		}
	}
	// Keep the cursor current, so a live preview scrolls with the pane.
	for _, p := range panes {
		if sc, ok := out[p.ID]; ok {
			p.CursorX, p.CursorY = sc.CursorX, sc.CursorY
		}
	}
}

// drawPreview draws the preview of the selected row in a width×height area
// at screen row y.
func (m *model) drawPreview(b *strings.Builder, y, width, height int) {
	c := newCanvas(width, height)
	m.previewY = y
	if n := m.selected(); n != nil && width >= 3 && height >= 3 {
		panes := m.previewPanes(n, width)
		m.loadScreens(panes)
		var failed error
		for _, p := range panes {
			if err := m.screens[p.ID].err; err != nil {
				failed = err
			}
		}
		switch {
		case failed != nil:
			c.box(0, 0, width-1, height-1, styleBorder)
			c.text(2, 1, width-1, "preview unavailable: "+clean(failed.Error()), styleError)
		case n.kind == paneRow || n.kind == jobRow:
			m.drawPane(c, n.pane, 0, 0, width-1, height-1)
			m.addHit(0, 0, width-1, height-1, n.target())
		case n.kind == windowRow:
			m.drawWindow(c, n.session.ID, n.window, 0, 0, width-1, height-1, styleActiveBorder, true)
		default:
			m.drawSession(c, n.session)
		}
	}
	c.draw(b, y)
}

// drawPane draws one pane filling the box (x0, y0)–(x1, y1).
func (m *model) drawPane(c *canvas, p *tmux.Pane, x0, y0, x1, y1 int) {
	c.blit(x0+1, y0+1, x1-x0-1, y1-y0-1, m.screens[p.ID].cells, p.CursorX, p.CursorY)
	c.box(x0, y0, x1, y1, styleActiveBorder)
	c.text(x0+2, y0, x1-1, " "+p.ID+" "+clean(p.Command)+" ", styleActiveLabel)
}

// addHit makes a box of the preview canvas clickable.
func (m *model) addHit(x0, y0, x1, y1 int, t tmux.Target) {
	m.hits = append(m.hits, hit{x0: x0, y0: y0 + m.previewY, x1: x1, y1: y1 + m.previewY, target: t})
}

// drawWindow draws a window's panes in their layout, scaled into the box
// (x0, y0)–(x1, y1). The active pane's border gets activeStyle. With
// paneDetail, each pane gets its id and command on its top border and is
// clickable.
func (m *model) drawWindow(c *canvas, sessionID string, w *tmux.Window, x0, y0, x1, y1 int, activeStyle string, paneDetail bool) {
	rects := paneRects(w, x0, y0, x1, y1)
	for _, r := range rects {
		c.blit(r.x0+1, r.y0+1, r.x1-r.x0-1, r.y1-r.y0-1, m.screens[r.p.ID].cells, r.p.CursorX, r.p.CursorY)
	}
	for _, r := range rects {
		c.box(r.x0, r.y0, r.x1, r.y1, styleBorder)
	}
	for _, r := range rects {
		if r.p.Active && activeStyle != "" && len(rects) > 1 {
			c.box(r.x0, r.y0, r.x1, r.y1, activeStyle)
		}
	}
	if paneDetail {
		for _, r := range rects {
			m.addHit(r.x0, r.y0, r.x1, r.y1, tmux.Target{Session: sessionID, Window: w.ID, Pane: r.p.ID})
			style := styleLabel
			if r.p.Active {
				style = styleActiveLabel
			}
			c.text(r.x0+2, r.y0, r.x1-1, " "+r.p.ID+" "+clean(r.p.Command)+" ", style)
		}
	}
}

// paneRect is where a pane is drawn: its box, borders included.
type paneRect struct {
	p              *tmux.Pane
	x0, y0, x1, y1 int
}

// paneRects places the panes a window shows in its layout, scaled into the
// box (x0, y0)–(x1, y1).
func paneRects(w *tmux.Window, x0, y0, x1, y1 int) []paneRect {
	innerW, innerH := x1-x0-1, y1-y0-1
	if innerW < 1 || innerH < 1 {
		return nil
	}
	// Window columns 0..Width-1 map into the box; -1 and Width are its edges,
	// so a pane's borders are the columns just outside it.
	ww, wh := max(1, w.Width), max(1, w.Height)
	sx := func(x int) int {
		switch {
		case x < 0:
			return x0
		case x >= ww:
			return x1
		}
		return x0 + 1 + x*innerW/ww
	}
	sy := func(y int) int {
		switch {
		case y < 0:
			return y0
		case y >= wh:
			return y1
		}
		return y0 + 1 + y*innerH/wh
	}
	var rects []paneRect
	for _, p := range layoutPanes(w) {
		left, top, width, height := p.Left, p.Top, p.Width, p.Height
		if w.Zoomed {
			left, top, width, height = 0, 0, ww, wh
		}
		rects = append(rects, paneRect{p, sx(left - 1), sy(top - 1), sx(left + width), sy(top + height)})
	}
	return rects
}

// drawSession draws a session's windows side by side, as many as fit, with
// clickable arrows at the edges when some are hidden.
func (m *model) drawSession(c *canvas, s *tmux.Session) {
	ws, left, right := m.sessionView(s, c.w)
	if len(ws) == 0 {
		return
	}
	x, end := 0, c.w
	if left {
		m.drawArrow(c, 0, '<', -1)
		x = arrowBox
	}
	if right {
		end = c.w - arrowBox
		m.drawArrow(c, end, '>', 1)
	}
	each := (end - x) / len(ws)
	for i, w := range ws {
		x0, x1 := x+i*each, x+(i+1)*each-1
		if i == len(ws)-1 {
			x1 = end - 1
		}
		m.drawWindow(c, s.ID, w, x0, 0, x1, c.h-1, "", false)
		m.addHit(x0, 0, x1, c.h-1, tmux.Target{Session: s.ID, Window: w.ID})
		style := styleLabel
		if w.Active {
			style = styleActiveLabel
			c.box(x0, 0, x1, c.h-1, styleActiveBorder)
		}
		c.text(x0+2, 0, x1-1, fmt.Sprintf(" %d: %s ", w.Index, clean(w.Name)), style)
	}
}

// drawArrow draws a scroll arrow box at column x that scrolls the session
// preview by scroll windows when clicked.
func (m *model) drawArrow(c *canvas, x int, r rune, scroll int) {
	c.box(x, 0, x+arrowBox-1, c.h-1, styleBorder)
	c.set(x+1, c.h/2, r, styleLabel)
	m.hits = append(m.hits, hit{x0: x, y0: m.previewY, x1: x + arrowBox - 1, y1: m.previewY + c.h - 1, scroll: scroll})
}
