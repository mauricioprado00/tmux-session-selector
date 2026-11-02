package ui

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

func TestCuts(t *testing.T) {
	if got := cuts(120, []float64{1, 1, 1}); !reflect.DeepEqual(got, []int{0, 40, 80, 120}) {
		t.Errorf("equal cuts = %v", got)
	}
	if got := cuts(100, []float64{1, 3}); !reflect.DeepEqual(got, []int{0, 25, 100}) {
		t.Errorf("weighted cuts = %v", got)
	}
}

func TestWindowSizes(t *testing.T) {
	s := windowSizes{}
	w := &tmux.Window{ID: "@1"}
	s.change("@1", false, 1, false)
	s.change("@1", false, 1, false)
	if got := s.of(w, false); got != 1.5625 || s.of(w, true) != 1 {
		t.Errorf("after two steps up: normal %v, middle %v", got, s.of(w, true))
	}
	for i := 0; i < 20; i++ {
		s.change("@1", true, -1, false)
	}
	if got := s.of(w, true); got != weight(-maxSteps) {
		t.Errorf("shrinking stops at %v, got %v", weight(-maxSteps), got)
	}
	s.change("@1", true, 0, true)
	s.change("@1", false, -2, false)
	if _, ok := s["@1"]; ok || s.of(w, false) != 1 {
		t.Errorf("back to 1 both ways, the window should be forgotten: %v", s)
	}
	var none windowSizes
	if none.of(w, true) != 1 {
		t.Error("no sizes should mean size 1")
	}
}

func TestSizedEvenLayout(t *testing.T) {
	// A full 2x2 grid has no cell to spare: a grown window stretches its
	// column and row, and the window sharing them, like % in an HTML table.
	ws := windows(4)
	boxes := evenLayout(ws, 120, 40, windowSizes{"@0": {normal: 2}})
	if width(boxes[0]) <= width(boxes[1]) || height(boxes[0]) <= height(boxes[2]) ||
		width(boxes[2]) != width(boxes[0]) || width(boxes[0])+width(boxes[1]) != 120 {
		t.Errorf("w0 grown in a full grid: %+v", boxes)
	}
	// Shrinking narrows too.
	boxes = evenLayout(ws, 120, 40, windowSizes{"@0": {normal: -3}})
	if width(boxes[0]) >= width(boxes[1]) {
		t.Errorf("w0 shrunk: %+v", boxes)
	}

	// 5 windows on 3x2 leave a cell empty: w0 at 2x snaps to two cells and
	// the others move along to fill the grid.
	ws = windows(5)
	boxes = evenLayout(ws, 120, 40, windowSizes{"@0": {normal: 3}})
	want := []cellBox{{0, 0, 79, 19}, {80, 0, 119, 19}, {0, 20, 39, 39}, {40, 20, 79, 39}, {80, 20, 119, 39}}
	if !reflect.DeepEqual(boxes, want) {
		t.Errorf("w0 snapped beside:\n got %+v\nwant %+v", boxes, want)
	}
	// Two windows at 2x and one spare cell: only the larger snaps; the
	// other stretches its column and row instead.
	boxes = evenLayout(ws, 120, 40, windowSizes{"@0": {normal: 3}, "@3": {normal: 4}})
	if width(boxes[3]) < 60 || width(boxes[0]) > width(boxes[3]) {
		t.Errorf("w3, larger, should be the one with two cells: %+v", boxes)
	}
}

func TestSnapDirection(t *testing.T) {
	// Tall windows on a tall screen: the second cell goes below.
	ws := windows(5)
	for _, w := range ws {
		w.Width, w.Height = 40, 60
	}
	free := [][]bool{{true, true, true}, {true, true, true}}
	order := [][2]int{{0, 0}, {1, 0}, {2, 0}, {0, 1}, {1, 1}, {2, 1}}
	rects, _ := packWindows(ws, free, order, 40, 20, windowSizes{"@0": {normal: 3}})
	if rects[0] != (cellRect{0, 0, 0, 1}) || rects[1] != (cellRect{1, 0, 1, 0}) || rects[4] != (cellRect{2, 1, 2, 1}) {
		t.Errorf("rects = %+v, want w0 down the first column", rects)
	}
}

func TestSizedCentredLayout(t *testing.T) {
	ws := windows(5)
	plain := overviewLayout(ws, 2, 120, 40, nil)
	// The centre's middle size grows the centre; its normal size does not.
	grown := overviewLayout(ws, 2, 120, 40, windowSizes{"@2": {normal: -6, middle: 3}})
	if width(grown[2]) <= width(plain[2]) || height(grown[2]) <= height(plain[2]) {
		t.Errorf("centre grown with its middle size: %+v, was %+v", grown[2], plain[2])
	}
	// Out of the centre, w2 has its normal size again.
	away := overviewLayout(ws, 0, 120, 40, windowSizes{"@2": {middle: 3}})
	if !reflect.DeepEqual(away, overviewLayout(ws, 0, 120, 40, nil)) {
		t.Errorf("w2's middle size should not apply out of the centre: %+v", away)
	}
	// The ring has empty cells: the top left window snaps to two cells
	// along the top, and the next window moves along.
	snapped := overviewLayout(ws, 2, 120, 40, windowSizes{"@0": {normal: 3}})
	if snapped[0] != (cellBox{0, 0, 59, 9}) || snapped[1] != (cellBox{60, 0, 89, 9}) {
		t.Errorf("snapped = %+v", snapped)
	}
	// Six steps would be a 2x2 block, which a ring one cell deep has no room
	// for: it keeps two cells and stretches them.
	block := overviewLayout(ws, 2, 120, 40, windowSizes{"@0": {normal: 6}})
	if width(block[0]) <= width(snapped[0]) || height(block[0]) <= height(snapped[0]) || block[0].y1 >= block[2].y0 {
		t.Errorf("block = %+v, snapped %+v", block[0], snapped[0])
	}
	// Still a grid a live window can be cut into.
	ws = windows(12)
	for i, w := range ws {
		w.Layout = "0000,80x24,0,0," + string(rune('0'+i%10))
	}
	var items []placed
	for i, box := range overviewLayout(ws, 8, 200, 60, windowSizes{"@8": {middle: 2}, "@3": {normal: 4}}) {
		items = append(items, placed{box, ws[i]})
	}
	if guillotine(items) == nil {
		t.Error("a sized centred overview should still cut into rows and columns")
	}
}

func width(b cellBox) int  { return b.x1 - b.x0 + 1 }
func height(b cellBox) int { return b.y1 - b.y0 + 1 }

func TestResizeKeys(t *testing.T) {
	s := &tmux.Session{ID: "$1", Name: "work", Windows: windows(5)}
	roots, total := buildTree([]*tmux.Session{s}, tmux.Location{}, "")
	path := filepath.Join(t.TempDir(), "state")
	// A saved size for a window that has since closed.
	if err := saveSetting(path, sizeKey+"@99", "2,2"); err != nil {
		t.Fatal(err)
	}
	m := &model{roots: roots, total: total, screens: map[string]screen{}, stateFile: path,
		sizes: readSizes(readState(path))}
	m.setQuery()
	m.handle(event{key: keyOverview}) // even, focus on w0
	key := func(r rune) { m.handle(event{key: keyRune, r: r}) }
	key('+')
	key('+')
	m.handle(event{key: keyOverview}) // centre w0
	key('-')
	if got := m.sizes["@0"]; got != (winSize{normal: 2, middle: -1}) {
		t.Errorf("sizes = %+v, want normal +2, middle -1", got)
	}
	saved := readState(path)
	if saved[sizeKey+"@0"] != "2,-1" || saved[sizeKey+"@99"] != "" {
		t.Errorf("saved = %v, want w0's sizes and none for the closed window", saved)
	}
	if got := readSizes(saved); !reflect.DeepEqual(got, m.sizes) {
		t.Errorf("read back %v, want %v", got, m.sizes)
	}
	key('=') // resets the middle size only
	m.handle(event{key: keyOverview})
	if got := m.sizes["@0"]; got != (winSize{normal: 2}) {
		t.Errorf("after = in the centre: %+v", got)
	}
}

func TestSnapBlock(t *testing.T) {
	// 20 windows: an 8x8 grid, its ring two cells deep, with room to spare.
	ws := windows(20)
	plain := overviewLayout(ws, 0, 160, 80, nil)
	block := overviewLayout(ws, 0, 160, 80, windowSizes{"@1": {normal: 6}})
	if width(block[1]) != 2*width(plain[1]) || height(block[1]) != 2*height(plain[1]) {
		t.Errorf("w1 at six steps = %+v, want a 2x2 block of %+v", block[1], plain[1])
	}
	for i, b := range block {
		for j, o := range block {
			if i < j && b.x0 <= o.x1 && o.x0 <= b.x1 && b.y0 <= o.y1 && o.y0 <= b.y1 {
				t.Errorf("w%d %+v overlaps w%d %+v", i, b, j, o)
			}
		}
	}
}

func TestResetAll(t *testing.T) {
	s := &tmux.Session{ID: "$1", Name: "work", Windows: windows(3)}
	roots, total := buildTree([]*tmux.Session{s}, tmux.Location{}, "")
	path := filepath.Join(t.TempDir(), "state")
	m := &model{roots: roots, total: total, screens: map[string]screen{}, stateFile: path}
	m.setQuery()
	m.handle(event{key: keyOverview}) // even, focus on w0
	key := func(r rune) { m.handle(event{key: keyRune, r: r}) }
	key('+')
	m.handle(event{key: keyTab})
	key('-')
	m.handle(event{key: keyOverview}) // centre w1
	key('+')
	if len(m.sizes) != 2 {
		t.Fatalf("sizes = %v", m.sizes)
	}
	key('=') // w1's middle size back to 1; the rest stays
	if got := m.sizes["@1"]; got != (winSize{normal: -1}) || len(m.sizes) != 2 || m.status != "" {
		t.Fatalf("after one =: %v, status %q", m.sizes, m.status)
	}
	key('=') // already at 1: everything
	if len(m.sizes) != 0 || m.status == "" {
		t.Errorf("after = =: %v, status %q", m.sizes, m.status)
	}
	for k := range readState(path) {
		if strings.HasPrefix(k, sizeKey) {
			t.Errorf("saved size left: %s", k)
		}
	}
}
