package tmux

import (
	"reflect"
	"strings"
	"testing"
)

func TestControlRead(t *testing.T) {
	long := "%output %9 " + strings.Repeat("x", 20000) + "\n"
	out := "%begin 1 2 0\n%output %8 not a notification\n%end 1 2 0\n" +
		"%session-changed $0 a\n" +
		"%output %0 echo hi\\015\\012\n" + long +
		"%output %0 more\n"
	c := newControl()
	c.read(strings.NewReader(out))
	<-c.News
	panes, windows, exited := c.Take()
	if !reflect.DeepEqual(panes, map[string]bool{"%0": true, "%9": true}) || windows || !exited {
		t.Errorf("panes %v, windows %v, exited %v", panes, windows, exited)
	}
	if panes, _, _ := c.Take(); len(panes) != 0 {
		t.Errorf("a second Take should be empty: %v", panes)
	}

	c = newControl()
	c.read(strings.NewReader("%layout-change @1 abc def *\n"))
	if _, windows, _ := c.Take(); !windows {
		t.Error("a layout change is a change of windows")
	}
	c = newControl()
	c.read(strings.NewReader("%begin 1 2 1\nparse error\n%error 1 2 1\n%subscription-changed tss $0 @1 1 %3 : x\n"))
	if _, windows, _ := c.Take(); !windows {
		t.Error("a subscription change after an error reply")
	}
}
