package tmux

import (
	"reflect"
	"testing"
)

// sample is a layout as tmux 3.4 printed it: one pane on the left, two
// stacked on the right.
const sample = "77dd,100x30,0,0{50x30,0,0,0,49x30,51,0[49x15,51,0,1,49x14,51,16,2]}"

func TestParseLayout(t *testing.T) {
	l, err := ParseLayout(sample)
	if err != nil {
		t.Fatal(err)
	}
	if !l.SideBySide || len(l.Children) != 2 || l.Children[1].SideBySide || !reflect.DeepEqual(l.Panes(), []int{0, 1, 2}) {
		t.Errorf("parsed %+v", l)
	}
	if got := l.String(); got != sample {
		t.Errorf("String = %s, want %s (tmux's checksum)", got, sample)
	}
	for _, bad := range []string{"", "77dd", "77dd,100x30,0,0", "77dd,100x30,0,0{50x30,0,0,0", "77dd,1x1,0,0,0junk"} {
		if _, err := ParseLayout(bad); err == nil {
			t.Errorf("ParseLayout(%q) should fail", bad)
		}
	}
}

func TestFit(t *testing.T) {
	l, _ := ParseLayout(sample)
	f, err := l.Fit(10, 5, 41, 21)
	if err != nil {
		t.Fatal(err)
	}
	want := "41x21,10,5{20x21,10,5,0,20x21,31,5[20x10,31,5,1,20x10,31,16,2]}"
	if got := f.String()[5:]; got != want {
		t.Errorf("Fit = %s, want %s", got, want)
	}
	if _, err := l.Fit(0, 0, 2, 2); err == nil {
		t.Error("two cells cannot hold three panes side by side and stacked")
	}
}

func TestShare(t *testing.T) {
	tests := []struct {
		total   int
		weights []int
		want    []int
	}{
		{100, []int{1, 1}, []int{50, 49}},
		{100, []int{1, 2, 1}, []int{25, 49, 24}},
		{10, []int{1, 1, 1, 1, 1}, []int{2, 1, 1, 1, 1}},
		{7, []int{100, 1, 1}, []int{3, 1, 1}},
	}
	for _, tt := range tests {
		if got, err := Share(tt.total, tt.weights); err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Share(%d, %v) = %v, %v; want %v", tt.total, tt.weights, got, err, tt.want)
		}
	}
	if _, err := Share(4, []int{1, 1, 1}); err == nil {
		t.Error("4 cells cannot hold 3 panes and 2 borders")
	}
}
