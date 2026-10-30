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
	boxes := matrixLayout(windows(5), 2, 120, 40)
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
	boxes = matrixLayout(windows(20), 0, 160, 80)
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

func TestMatrixKeys(t *testing.T) {
	s := &tmux.Session{ID: "$1", Name: "work", Windows: windows(5)}
	s.Windows[1].Active = true
	roots, total := buildTree([]*tmux.Session{s}, tmux.Location{}, "")
	screens := map[string]screen{}
	for _, w := range s.Windows {
		screens[w.Panes[0].ID] = screen{}
	}
	m := &model{roots: roots, total: total, screens: screens}
	m.setQuery()

	m.handle(event{key: keyMatrix}) // from the session row: even, on its active window
	if m.matrix == nil || m.matrix.center != nil || m.matrix.focus != 1 {
		t.Fatalf("matrix = %+v", m.matrix)
	}
	m.render(120, 42)
	m.handle(event{key: keyExpand})
	if m.matrix.focus != 2 {
		t.Errorf("Right should focus w2, got w%d", m.matrix.focus)
	}
	m.handle(typed('j'))
	if m.matrix.focus != 4 {
		t.Errorf("j should focus w4 below, got w%d", m.matrix.focus)
	}
	if done, target, _ := m.handle(event{key: keyEnter}); !done || target.Window != "@4" {
		t.Errorf("Enter should switch to w4, got %v %+v", done, target)
	}

	m.handle(event{key: keyCancel})
	if m.matrix != nil {
		t.Error("Esc should close the matrix")
	}
	m.selectRow(t, "w3")
	m.handle(event{key: keyMatrix})
	if m.matrix.center != s.Windows[3] || m.matrix.focus != 3 {
		t.Fatalf("from a window row the matrix centres on it: %+v", m.matrix)
	}
	m.render(120, 42)
	m.handle(event{key: keyUp})
	if m.matrix.focus == 3 {
		t.Error("Up from the centre should focus a window above it")
	}
	// Click w0, the top left cell (screen row 1 is its top border).
	m.handle(event{key: keyMouse, x: 5, y: 3, press: true})
	if m.matrix.focus != 0 {
		t.Errorf("a click should focus w0, got w%d", m.matrix.focus)
	}
	m.handle(typed('o'))
	if m.matrix.center != s.Windows[0] {
		t.Errorf("o should centre the window in focus, w0, got %+v", m.matrix.center)
	}
	m.handle(event{key: keyMatrix})
	if m.matrix == nil || m.matrix.center != nil {
		t.Errorf("Ctrl-O on the centred window should go back to the even grid")
	}
	m.handle(typed('o'))
	if m.matrix.center != s.Windows[0] || m.matrix.focus != 0 {
		t.Errorf("o should centre w0 again, keeping the focus")
	}
	m.handle(typed('q'))
	if m.matrix != nil {
		t.Error("q should close the matrix")
	}
}
