package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

func TestSGR(t *testing.T) {
	tests := []struct {
		seqs []string
		want string
	}{
		{[]string{"31"}, "31"},
		{[]string{"1", "38;5;33"}, "1;38;5;33"},
		{[]string{"1;38;5;33", "39"}, "1"},
		{[]string{"30", "42", "0"}, ""},
		{[]string{"38;2;1;2;3;48;5;7;4"}, "4;38;2;1;2;3;48;5;7"},
		{[]string{"1;2", "22"}, ""},
		{[]string{"7", "27", "3"}, "3"},
		{[]string{"38:5:208"}, "38:5:208"},
		{[]string{"4:3", "4:0"}, ""},
		{[]string{"31", ""}, ""}, // ESC [ m resets
		{[]string{"38;5"}, ""},   // incomplete colour is ignored
	}
	for _, tt := range tests {
		var st sgr
		for _, s := range tt.seqs {
			st.apply(s)
		}
		if got := st.String(); got != tt.want {
			t.Errorf("apply(%q) = %q, want %q", tt.seqs, got, tt.want)
		}
	}
}

func TestParseScreen(t *testing.T) {
	s := parseScreen("\x1b[31mab\x1b[39m c\n\x1b]8;;http://x\x1b\\日\tz\n")
	if len(s) != 2 {
		t.Fatalf("got %d lines", len(s))
	}
	want := []cell{{'a', "31"}, {'b', "31"}, {' ', ""}, {'c', ""}}
	if !equalCells(s[0], want) {
		t.Errorf("line 0 = %v", s[0])
	}
	want = []cell{{'日', ""}, {0, ""}, {' ', ""}, {'z', ""}}
	if !equalCells(s[1], want) {
		t.Errorf("line 1 = %v", s[1])
	}
}

func equalCells(a, b []cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// rows returns the canvas characters, one string per row.
func (c *canvas) rows() []string {
	var out []string
	for _, row := range c.cells {
		var b strings.Builder
		for _, cl := range row {
			if cl.r != 0 {
				b.WriteRune(cl.r)
			}
		}
		out = append(out, b.String())
	}
	return out
}

func TestBoxJunctions(t *testing.T) {
	c := newCanvas(5, 3)
	c.box(0, 0, 2, 2, "")
	c.box(2, 0, 4, 2, "")
	want := []string{"┌─┬─┐", "│ │ │", "└─┴─┘"}
	if got := c.rows(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestDrawWindowLayout(t *testing.T) {
	// A 21×10 window split into a left pane and two stacked right panes.
	w := &tmux.Window{Width: 21, Height: 10, Panes: []*tmux.Pane{
		{ID: "%0", Left: 0, Top: 0, Width: 10, Height: 10, Active: true},
		{ID: "%1", Left: 11, Top: 0, Width: 10, Height: 4},
		{ID: "%2", Left: 11, Top: 5, Width: 10, Height: 5},
	}}
	m := &model{screens: map[string]screen{
		"%0": {cells: parseScreen("left\n")},
		"%1": {cells: parseScreen("top\n")},
		"%2": {cells: parseScreen("bottom\n")},
	}}
	c := newCanvas(23, 12)
	m.drawWindow(c, "$1", w, 0, 0, 22, 11, styleActiveBorder, false)
	want := []string{
		"┌──────────┬──────────┐",
		"│left      │top       │",
		"│          │          │",
		"│          │          │",
		"│          │          │",
		"│          ├──────────┤",
		"│          │bottom    │",
		"│          │          │",
		"│          │          │",
		"│          │          │",
		"│          │          │",
		"└──────────┴──────────┘",
	}
	if got := c.rows(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if c.cells[1][0].style != styleActiveBorder || c.cells[1][22].style == styleActiveBorder {
		t.Error("only the active pane's border should be highlighted")
	}
}

func TestBlitFollowsCursor(t *testing.T) {
	c := newCanvas(3, 2)
	scr := parseScreen("a\nb\nc\nd\n")
	c.blit(0, 0, 3, 2, scr, 0, 3)
	if got := strings.Join(c.rows(), "|"); got != "c  |d  " {
		t.Errorf("got %q, want the two lines ending at the cursor", got)
	}
}

func TestSessionView(t *testing.T) {
	s := &tmux.Session{}
	for i := 0; i < 7; i++ {
		s.Windows = append(s.Windows, &tmux.Window{Index: i, Active: i == 5, Width: 80, Height: 24})
	}
	m := &model{screens: map[string]screen{}}
	span := func(width int) string {
		ws, left, right := m.sessionView(s, width)
		return fmt.Sprintf("%v %d-%d %v", left, ws[0].Index, ws[len(ws)-1].Index, right)
	}
	// 72 columns: three boxes of 24; window 5 centred.
	if got := span(72); got != "true 4-6 false" {
		t.Errorf("initial view = %s", got)
	}
	m.winOffset -= 3
	if got := span(72); got != "true 1-3 true" {
		t.Errorf("after scrolling left = %s", got)
	}
	m.winOffset -= 5
	if got := span(72); got != "false 0-2 true" {
		t.Errorf("scrolling stops at the first window, got %s", got)
	}
	if got := span(167); got != "true 0-5 true" && got != "false 0-5 true" {
		t.Errorf("167 columns fit 6 of 7 windows, got %s", got)
	}
	if got := span(168); got != "false 0-6 false" {
		t.Errorf("all windows fit = %s", got)
	}
}

func TestSessionArrows(t *testing.T) {
	s := &tmux.Session{ID: "$1"}
	for i := 0; i < 7; i++ {
		s.Windows = append(s.Windows, &tmux.Window{ID: fmt.Sprint("@", i), Index: i, Active: i == 0, Width: 80, Height: 24})
	}
	m := &model{screens: map[string]screen{}, previewY: 10}
	c := newCanvas(66, 8)
	m.drawSession(c, s)
	rows := c.rows()
	if !strings.HasSuffix(rows[4], "│>│") || strings.Contains(rows[4], "<") {
		t.Errorf("want only a right arrow, got %q", rows[4])
	}
	var arrow *hit
	for i := range m.hits {
		if m.hits[i].scroll != 0 {
			arrow = &m.hits[i]
		}
	}
	if arrow == nil || arrow.scroll != 1 || arrow.x0 != 63 || arrow.y0 != 10 || arrow.y1 != 17 {
		t.Errorf("right arrow hit = %+v", arrow)
	}
}
