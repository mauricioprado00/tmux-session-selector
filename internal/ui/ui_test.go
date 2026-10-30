package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mauricioprado00/tmux-session-selector/internal/filter"
	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

func TestDecode(t *testing.T) {
	tests := []struct {
		in   string
		want []event
	}{
		{"ab", []event{{keyRune, 'a'}, {keyRune, 'b'}}},
		{"é", []event{{keyRune, 'é'}}},
		{"\r", []event{{key: keyEnter}}},
		{"\n", []event{{key: keyDown}}},
		{"\x1b", []event{{key: keyCancel}}},
		{"\x1b[A\x1bOB", []event{{key: keyUp}, {key: keyDown}}},
		{"\x1b[1;5C", []event{{key: keyExpand}}},
		{"\x1b[Z", []event{{key: keyCollapse}}},
		{"\x1b[5~\x1b[6~", []event{{key: keyPageUp}, {key: keyPageDown}}},
		{"\x1b[1~\x1b[4~\x1b[H\x1b[F", []event{{key: keyHome}, {key: keyEnd}, {key: keyHome}, {key: keyEnd}}},
		{"\x1b[2~x", []event{{keyRune, 'x'}}}, // Insert is ignored
		{"\x7f\x17\x15\x12\x16\x07", []event{{key: keyBackspace}, {key: keyDeleteWord}, {key: keyClear},
			{key: keyToggleRegex}, {key: keyTogglePreview}, {key: keyCancel}}},
	}
	for _, tt := range tests {
		if got := decode([]byte(tt.in)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("decode(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func testTree() []*node {
	pane := func(id, cmd string) *tmux.Pane { return &tmux.Pane{ID: id, Command: cmd, Path: "/home/me/src"} }
	sessions := []*tmux.Session{
		{ID: "$1", Name: "api", Windows: []*tmux.Window{
			{ID: "@1", Index: 0, Name: "editor", Panes: []*tmux.Pane{pane("%1", "nvim"), pane("%2", "zsh")}},
			{ID: "@2", Index: 1, Name: "server", Panes: []*tmux.Pane{pane("%3", "go")}},
		}},
		{ID: "$2", Name: "blog", Windows: []*tmux.Window{
			{ID: "@3", Index: 0, Name: "posts", Panes: []*tmux.Pane{pane("%4", "zsh")}},
		}},
	}
	roots, _ := buildTree(sessions, tmux.Location{Session: "$1", Window: "@1", Pane: "%2"}, "/home/me")
	return roots
}

// names lists visible rows as name, command or pane id, with context rows in
// parentheses.
func names(rows []row) []string {
	var out []string
	for _, r := range rows {
		s := r.n.name
		if r.n.kind == paneRow {
			s = r.n.pane.ID
		}
		if r.context {
			s = "(" + s + ")"
		}
		out = append(out, s)
	}
	return out
}

func TestVisible(t *testing.T) {
	roots := testTree()
	tests := []struct {
		query   string
		want    []string
		matches int
	}{
		{"", []string{"api", "editor", "server", "blog", "posts"}, 5},
		{"nvim", []string{"(api)", "(editor)", "%1"}, 1},
		{"zsh", []string{"(api)", "(editor)", "%2", "(blog)", "posts"}, 2},
		{"api", []string{"api", "editor", "%1", "%2", "server"}, 5},
		{"src", []string{"(api)", "(editor)", "%1", "%2", "server", "(blog)", "posts"}, 4},
		{"!api", []string{"blog", "posts"}, 2},
		{"nothing", nil, 0},
	}
	for _, tt := range tests {
		m, _ := filter.New(tt.query, filter.Words)
		rows, matches := visible(roots, m)
		if got := names(rows); !reflect.DeepEqual(got, tt.want) || matches != tt.matches {
			t.Errorf("visible(%q) = %q, %d; want %q, %d", tt.query, got, matches, tt.want, tt.matches)
		}
	}
}

func TestBuildTree(t *testing.T) {
	roots := testTree()
	api, editor, server := roots[0], roots[0].children[0], roots[0].children[1]
	if !api.current || !editor.current || !editor.children[1].current || editor.children[0].current || roots[1].current {
		t.Error("current location marked wrongly")
	}
	if server.pane == nil || server.path != "~/src" || len(server.children) != 0 {
		t.Errorf("single-pane window should stand for its pane: %+v", server)
	}
	if got := editor.children[0].target(); got != (tmux.Target{Session: "$1", Window: "@1", Pane: "%1"}) {
		t.Errorf("pane target = %+v", got)
	}
	if got := server.target(); got != (tmux.Target{Session: "$1", Window: "@2"}) {
		t.Errorf("window target = %+v", got)
	}
}

func TestModelSelection(t *testing.T) {
	m := &model{roots: testTree(), total: 7}
	m.setQuery()
	m.selectCurrent()
	if n := m.selected(); n.name != "editor" {
		t.Fatalf("initial selection = %q, want the current window", n.name)
	}
	for _, r := range "zsh" {
		m.handle(event{key: keyRune, r: r})
	}
	if n := m.selected(); n.kind != paneRow || n.pane.ID != "%2" {
		t.Errorf("after typing, selection = %+v, want the first match", n)
	}
	m.handle(event{key: keyClear})
	if n := m.selected(); n.name != "editor" {
		t.Errorf("after clearing, selection = %q, want the hidden pane's window", n.name)
	}
	m.handle(event{key: keyCollapse})
	if n := m.selected(); n.name != "api" {
		t.Errorf("collapse on a collapsed window should select its session, got %q", n.name)
	}
	m.handle(event{key: keyCollapse})
	if len(m.rows) != 3 {
		t.Errorf("collapsing api should leave 3 rows, got %q", names(m.rows))
	}
	m.query = []rune("po(st")
	m.mode = filter.Regex
	m.setQuery()
	if m.queryErr == "" || len(m.rows) != 3 {
		t.Errorf("invalid regex should keep the rows and set an error: %q %q", m.queryErr, names(m.rows))
	}
}

func TestDrawLine(t *testing.T) {
	strip := func(s string) string {
		var b strings.Builder
		for i := 0; i < len(s); i++ {
			if s[i] == 0x1b {
				for i < len(s) && !(s[i] >= 'A' && s[i] <= 'z' && s[i] != '[') {
					i++
				}
				continue
			}
			b.WriteByte(s[i])
		}
		return b.String()
	}
	tests := []struct {
		width int
		text  string
		fill  bool
		want  string
	}{
		{10, "hello", false, "hello"},
		{10, "hello", true, "hello     "},
		{5, "hello", false, "hello"},
		{4, "hello", false, "hel…"},
		{4, "日本語", false, "日…"},
		{5, "日本語", false, "日本…"},
	}
	for _, tt := range tests {
		var b strings.Builder
		drawLine(&b, 0, tt.width, "", tt.fill, span{text: tt.text})
		if got := strip(b.String()); got != tt.want {
			t.Errorf("drawLine(%q, %d) = %q, want %q", tt.text, tt.width, got, tt.want)
		}
	}
}

func TestClean(t *testing.T) {
	if got := clean("a\tb\x1b[31mc\x7f"); got != "a b?[31mc?" {
		t.Errorf("clean = %q", got)
	}
}

// BenchmarkKeystroke measures one query change on 500 panes, which the spec
// requires to fit in a frame (16 ms).
func BenchmarkKeystroke(b *testing.B) {
	var sessions []*tmux.Session
	for s := 0; s < 50; s++ {
		sess := &tmux.Session{ID: "$" + string(rune('a'+s%26)), Name: strings.Repeat("x", s%7) + "session"}
		for w := 0; w < 5; w++ {
			sess.Windows = append(sess.Windows, &tmux.Window{ID: "@w", Index: w, Name: "window", Panes: []*tmux.Pane{
				{ID: "%1", Command: "zsh", Path: "/home/me/src/project"},
				{ID: "%2", Command: "nvim", Path: "/home/me/src/other"},
			}})
		}
		sessions = append(sessions, sess)
	}
	roots, total := buildTree(sessions, tmux.Location{}, "/home/me")
	m := &model{roots: roots, total: total, captures: map[string]capture{}}
	m.setQuery()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.query = []rune("xx src vim")[:1+i%10]
		m.setQuery()
		m.render(200, 60)
	}
}
