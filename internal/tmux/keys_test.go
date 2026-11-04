package tmux

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInput(t *testing.T) {
	cases := []struct {
		in   string
		want []Key
	}{
		{"héllo", []Key{{"héllo", true}}},
		{"ab\rc", []Key{{"ab", true}, {"Enter", false}, {"c", true}}},
		{"\x1b[A\x1bOB\x1b[1;5C\x1b[1;2D", []Key{{"Up", false}, {"Down", false}, {"C-Right", false}, {"S-Left", false}}},
		{"\x1b[5~\x1b[3;3~\x1b[15~\x1bOP\x1b[Z", []Key{{"PPage", false}, {"M-DC", false}, {"F5", false}, {"F1", false}, {"BTab", false}}},
		{"\x01\x7f\t\x00\x1d", []Key{{"C-a", false}, {"BSpace", false}, {"Tab", false}, {"C-Space", false}, {"C-]", false}}},
		{"\x1bx\x1b \x1b", []Key{{"M-x", false}, {"M-Space", false}, {"Escape", false}}},
		{"\x1b[<0;10;5M", nil},
	}
	for _, c := range cases {
		if got := Input([]byte(c.in)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Input(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSendKeys(t *testing.T) {
	tm := server(t)
	pane := mustRun(t, tm, "new-session", "-d", "-P", "-F", "#{pane_id}", "-x", "80", "-y", "24", "cat -v")
	time.Sleep(200 * time.Millisecond)
	// Text ending in ; or \; and starting with -, which tmux would take for
	// the end of a command or an option.
	keys := []Key{{"a;", true}, {`b\;`, true}, {"-x", true}, {"M-;", false}, {"Enter", false}}
	if err := tm.SendKeys(pane, keys); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	out := mustRun(t, tm, "capture-pane", "-p", "-t", pane)
	if lines := strings.Split(out, "\n"); len(lines) < 2 || lines[1] != `a;b\;-x^[;` {
		t.Errorf("the pane got %q", out)
	}
}
