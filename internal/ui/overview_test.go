package ui

import (
	"fmt"
	"testing"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

func windows(n int) []*tmux.Window {
	var ws []*tmux.Window
	for i := 0; i < n; i++ {
		ws = append(ws, &tmux.Window{ID: fmt.Sprint("@", i), Index: i, Name: fmt.Sprint("w", i),
			Width: 80, Height: 24, Panes: []*tmux.Pane{{ID: fmt.Sprint("%", i), Width: 80, Height: 24}}})
	}
	return ws
}

func TestCentredLayout(t *testing.T) {
	boxes := overviewLayout(windows(5), 2, 120, 40)
	if got := boxes[2]; got != (cellBox{30, 10, 89, 29}) {
		t.Errorf("centre = %+v, want the middle half each way", got)
	}
	// The others go clockwise from the top left: top left, then along the top.
	want := []cellBox{{0, 0, 29, 9}, {30, 0, 59, 9}, {60, 0, 89, 9}, {90, 0, 119, 9}}
	for i, w := range []int{0, 1, 3, 4} {
		if boxes[w] != want[i] {
			t.Errorf("window %d at %+v, want %+v", w, boxes[w], want[i])
		}
	}
	// More windows than the 12 ring cells of a 4×4 grid: an 8×8 grid.
	boxes = overviewLayout(windows(20), 0, 160, 80)
	if got := boxes[0]; got != (cellBox{40, 20, 119, 59}) {
		t.Errorf("centre of 8x8 = %+v", got)
	}
	seen := map[cellBox]bool{}
	for _, b := range boxes {
		if seen[b] {
			t.Fatalf("two windows share %+v", b)
		}
		seen[b] = true
	}
}

func TestEvenLayout(t *testing.T) {
	boxes := evenLayout(windows(5), 120, 40)
	if len(boxes) != 5 || boxes[0].x0 != 0 || boxes[0].y0 != 0 {
		t.Fatalf("boxes = %+v", boxes)
	}
	// 5 windows of 80x24 on 120x40: 3 columns over 2 rows; the last row of 2
	// is spread across the full width.
	if boxes[2].x1 != 119 || boxes[3] != (cellBox{0, 20, 59, 39}) || boxes[4] != (cellBox{60, 20, 119, 39}) {
		t.Errorf("boxes = %+v", boxes)
	}
}

func TestOverviewKeys(t *testing.T) {
	s := &tmux.Session{ID: "$1", Name: "work", Windows: windows(5)}
	s.Windows[1].Active = true
	roots, total := buildTree([]*tmux.Session{s}, tmux.Location{}, "")
	screens := map[string]screen{}
	for _, w := range s.Windows {
		screens[w.Panes[0].ID] = screen{}
	}
	m := &model{roots: roots, total: total, screens: screens}
	m.setQuery()

	m.handle(event{key: keyOverview}) // from the session row: even, on its active window
	if m.overview == nil || m.overview.center != nil || m.overview.focus != 1 {
		t.Fatalf("overview = %+v", m.overview)
	}
	m.render(120, 42)
	m.handle(event{key: keyExpand})
	if m.overview.focus != 2 {
		t.Errorf("Right should focus w2, got w%d", m.overview.focus)
	}
	m.handle(typed('j'))
	if m.overview.focus != 4 {
		t.Errorf("j should focus w4 below, got w%d", m.overview.focus)
	}
	if done, target, _ := m.handle(event{key: keyEnter}); !done || target.Window != "@4" {
		t.Errorf("Enter should switch to w4, got %v %+v", done, target)
	}

	m.handle(event{key: keyCancel})
	if m.overview != nil {
		t.Error("Esc should close the overview")
	}
	m.selectRow(t, "w3")
	m.handle(event{key: keyOverview})
	if m.overview.center != s.Windows[3] || m.overview.focus != 3 {
		t.Fatalf("from a window row the overview centres on it: %+v", m.overview)
	}
	m.render(120, 42)
	m.handle(event{key: keyUp})
	if m.overview.focus == 3 {
		t.Error("Up from the centre should focus a window above it")
	}
	// Click w0, the top left cell (screen row 1 is its top border).
	m.handle(event{key: keyMouse, x: 5, y: 3, press: true})
	if m.overview.focus != 0 {
		t.Errorf("a click should focus w0, got w%d", m.overview.focus)
	}
	m.handle(typed('o'))
	if m.overview.center != s.Windows[0] {
		t.Errorf("o should centre the window in focus, w0, got %+v", m.overview.center)
	}
	m.handle(event{key: keyOverview})
	if m.overview == nil || m.overview.center != nil {
		t.Errorf("Ctrl-O on the centred window should go back to the even grid")
	}
	m.handle(typed('o'))
	if m.overview.center != s.Windows[0] || m.overview.focus != 0 {
		t.Errorf("o should centre w0 again, keeping the focus")
	}
	m.handle(typed('q'))
	if m.overview != nil {
		t.Error("q should close the overview")
	}
}

// TestCentreReachable checks that from every window next to the centre, the
// key towards the centre focuses it, and that the centre reaches every side.
func TestCentreReachable(t *testing.T) {
	ws := windows(12)
	ov := &overview{cells: overviewLayout(ws, 8, 240, 60)}
	c := ov.cells[8]
	dirs := []struct {
		name   string
		dx, dy int
	}{{"Right", 1, 0}, {"Left", -1, 0}, {"Down", 0, 1}, {"Up", 0, -1}}
	for i, b := range ov.cells {
		if i == 8 {
			continue
		}
		ov.focus = i
		for _, d := range dirs {
			// b touches the centre on the side d points to.
			touches := (d.dx > 0 && b.x1+1 == c.x0 && b.y1 >= c.y0 && b.y0 <= c.y1) ||
				(d.dx < 0 && b.x0-1 == c.x1 && b.y1 >= c.y0 && b.y0 <= c.y1) ||
				(d.dy > 0 && b.y1+1 == c.y0 && b.x1 >= c.x0 && b.x0 <= c.x1) ||
				(d.dy < 0 && b.y0-1 == c.y1 && b.x1 >= c.x0 && b.x0 <= c.x1)
			if got := ov.nearest(d.dx, d.dy); touches && got != 8 {
				t.Errorf("from w%d, %s should focus the centre, got w%d", i, d.name, got)
			}
		}
	}
	ov.focus = 8
	for _, d := range dirs {
		if got := ov.nearest(d.dx, d.dy); got == 8 {
			t.Errorf("from the centre, %s found nothing", d.name)
		}
	}
}

func TestOverviewNavigation(t *testing.T) {
	ws := windows(12)
	ws[9].Index, ws[10].Index, ws[11].Index = 17, 21, 30 // gaps in the numbering
	ov := &overview{session: &tmux.Session{Windows: ws}, cells: evenLayout(ws, 160, 60)}
	// 12 windows on 160x60: rows of the same width; w0 is top left.
	ov.focus = 0
	if got := ov.nearest(-1, 0); ov.cells[got].y0 != 0 || ov.cells[got].x1 != 159 {
		t.Errorf("Left from the first column should wrap to the end of the row, got w%d %+v", got, ov.cells[got])
	}
	if got := ov.nearest(0, -1); ov.cells[got].x0 != 0 || ov.cells[got].y1 != 59 {
		t.Errorf("Up from the top should wrap to the bottom of the column, got w%d", got)
	}

	m := &model{overview: ov}
	m.handle(event{key: keyTab})
	m.handle(event{key: keyTab})
	if ov.focus != 2 {
		t.Errorf("Tab Tab should focus w2, got w%d", ov.focus)
	}
	m.handle(event{key: keyBackTab})
	m.handle(event{key: keyBackTab})
	m.handle(event{key: keyBackTab})
	if ov.focus != 11 {
		t.Errorf("Shift-Tab should wrap to the last window, got w%d", ov.focus)
	}
	m.handle(typed('1'))
	m.handle(typed('7'))
	if ws[ov.focus].Index != 17 {
		t.Errorf("typing 1 7 should focus window 17, got %d", ws[ov.focus].Index)
	}
	m.handle(typed('5'))
	if ws[ov.focus].Index != 5 {
		t.Errorf("175 is no window, so 5 starts a new number; got %d", ws[ov.focus].Index)
	}
	ov.numberAt = ov.numberAt.Add(-2 * numberGap)
	m.handle(typed('3'))
	if ws[ov.focus].Index != 3 {
		t.Errorf("after a pause, 3 is a new number; got %d", ws[ov.focus].Index)
	}
	m.handle(event{key: keyEnd})
	if ov.focus != 11 {
		t.Errorf("End should focus the last window, got w%d", ov.focus)
	}
}

func TestTabStillExpandsInTheList(t *testing.T) {
	m := &model{roots: testTree(), total: 7}
	m.setQuery()
	m.handle(event{key: keyDown}) // editor
	m.handle(event{key: keyTab})
	if len(m.rows) != 7 {
		t.Errorf("Tab should expand editor, rows %q", names(m.rows))
	}
	m.handle(event{key: keyBackTab})
	if len(m.rows) != 5 {
		t.Errorf("Shift-Tab should collapse editor, rows %q", names(m.rows))
	}
}

// TestFocusPointThroughCentre replays moving through the centre window: the
// focus should come out in line with where it went in.
func TestFocusPointThroughCentre(t *testing.T) {
	ws := windows(12)
	ov := &overview{session: &tmux.Session{Windows: ws}, cells: overviewLayout(ws, 8, 240, 60)}
	// A 4x4 grid of 60x15 cells; the centre takes columns 1-2, rows 1-2.
	at := func(col, row int) int {
		for i, b := range ov.cells {
			if b.x0 == col*60 && b.y0 == row*15 && i != 8 {
				return i
			}
		}
		t.Fatalf("no window at column %d, row %d", col, row)
		return -1
	}
	tests := []struct {
		name   string
		from   int
		dx, dy int
		want   int
	}{
		{"up from below the right half", at(2, 3), 0, -1, at(2, 0)},
		{"up from below the left half", at(1, 3), 0, -1, at(1, 0)},
		{"down from above the right half", at(2, 0), 0, 1, at(2, 3)},
		{"right from left of the lower half", at(0, 2), 1, 0, at(3, 2)},
		{"left from right of the upper half", at(3, 1), -1, 0, at(0, 2)}, // (0, 1) is empty
		{"left from right of the lower half", at(3, 2), -1, 0, at(0, 2)},
	}
	for _, tt := range tests {
		ov.focus, ov.px, ov.py = tt.from, -1, -1
		ov.move(tt.dx, tt.dy)
		if ov.focus != 8 {
			t.Errorf("%s: first move should reach the centre, got w%d", tt.name, ov.focus)
			continue
		}
		ov.move(tt.dx, tt.dy)
		if ov.focus != tt.want {
			t.Errorf("%s: second move got w%d %+v, want w%d %+v", tt.name, ov.focus, ov.cells[ov.focus], tt.want, ov.cells[tt.want])
		}
	}
}
