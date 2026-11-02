package jobs

import (
	"fmt"
	"reflect"
	"testing"
)

// The pane's shell is 100 (its own group) with vim in the foreground, two
// stopped jobs (a pipeline counts as two processes of one job), and a make
// running in the background that started a compiler. A daemon that left the
// terminal (tpgid -1) and another shell's job are not this shell's.
const psOut = `
  100     1   100   300 Ss   -bash
  200   100   200   300 T    less /etc/passwd
  201   100   201   300 T    grep foo
  202   100   201   300 T    sort
  250   100   250   300 S    make -j4
  251   250   250   300 S    cc -c main.c
  300   100   300   300 S+   vim notes.md
  400   100   400    -1 Ss   some-daemon
  500     1   500   500 Ss   -zsh
  501   500   501   500 T    top
`

func TestParse(t *testing.T) {
	got := Parse(psOut, []int{100, 500, 999})
	want := map[int][]*Job{
		100: {
			{PID: 200, Stopped: true, Args: "less /etc/passwd"},
			{PID: 201, Stopped: true, Args: "grep foo"},
			{PID: 202, Stopped: true, Args: "sort"},
			{PID: 250, Args: "make -j4", Children: []*Job{{PID: 251, Args: "cc -c main.c"}}},
		},
		500: {{PID: 501, Stopped: true, Args: "top"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse = %s, want %s", dump(got), dump(want))
	}
}

// With a job in the foreground the shell's own group is in the background,
// but the shell is not a job.
func TestParseShellNotAJob(t *testing.T) {
	out := "100 1 100 300 Ss -bash\n300 100 300 300 S+ vim\n"
	if got := Parse(out, []int{100}); len(got) != 0 {
		t.Errorf("Parse = %s, want nothing", dump(got))
	}
}

func dump(m map[int][]*Job) string {
	var s string
	var walk func(js []*Job, indent string)
	walk = func(js []*Job, indent string) {
		for _, j := range js {
			s += indent + j.Args
			if j.Stopped {
				s += " (stopped)"
			}
			s += "\n"
			walk(j.Children, indent+"  ")
		}
	}
	for shell, js := range m {
		s += fmt.Sprintf("shell %d:\n", shell)
		walk(js, "  ")
	}
	return s
}
