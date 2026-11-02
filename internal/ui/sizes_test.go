package ui

import (
	"path/filepath"
	"reflect"
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
	ws := windows(4) // 2×2 on 120×40
	sizes := windowSizes{"@0": {normal: 4}}
	boxes := evenLayout(ws, 120, 40, sizes)
	w0, w1 := boxes[0].x1-boxes[0].x0+1, boxes[1].x1-boxes[1].x0+1
	h0, h2 := boxes[0].y1-boxes[0].y0+1, boxes[2].y1-boxes[2].y0+1
	if w0 <= w1 || h0 <= h2 || w0+w1 != 120 || h0+h2 != 40 {
		t.Errorf("w0 grown: widths %d+%d, heights %d+%d", w0, w1, h0, h2)
	}
	if boxes[2].x1-boxes[2].x0 != boxes[3].x1-boxes[3].x0 {
		t.Errorf("the other row should be unchanged: %+v", boxes)
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
	// Out of the centre, w2 has its normal size again, and w0 (top left)
	// is centred with size 1.
	away := overviewLayout(ws, 0, 120, 40, windowSizes{"@2": {normal: 0, middle: 3}})
	if away[0] != overviewLayout(ws, 0, 120, 40, nil)[0] {
		t.Errorf("w2's middle size should not apply out of the centre: %+v", away)
	}
	// A corner window grows both ways.
	corner := overviewLayout(ws, 2, 120, 40, windowSizes{"@0": {normal: 3}})
	if width(corner[0]) <= width(plain[0]) || height(corner[0]) <= height(plain[0]) {
		t.Errorf("corner window grown: %+v, was %+v", corner[0], plain[0])
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
