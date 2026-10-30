package ui

import (
	"fmt"
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
		{"ab", []event{{key: keyRune, r: 'a'}, {key: keyRune, r: 'b'}}},
		{"é", []event{{key: keyRune, r: 'é'}}},
		{"\r", []event{{key: keyEnter}}},
		{"\n", []event{{key: keyDown}}},
		{"\x1b", []event{{key: keyCancel}}},
		{"\x1b[A\x1bOB", []event{{key: keyUp}, {key: keyDown}}},
		{"\x1b[1;5C", []event{{key: keyExpand}}},
		{"\x1b[Z", []event{{key: keyCollapse}}},
		{"\x1b[5~\x1b[6~", []event{{key: keyPageUp}, {key: keyPageDown}}},
		{"\x1b[1~\x1b[4~\x1b[H\x1b[F", []event{{key: keyHome}, {key: keyEnd}, {key: keyHome}, {key: keyEnd}}},
		{"\x1b[2~x", []event{{key: keyRune, r: 'x'}}}, // Insert is ignored
		{"\x1bx\x1bO\x1b:", []event{{key: keyAlt, r: 'x'}, {key: keyAlt, r: 'O'}, {key: keyAlt, r: ':'}}},
		{"\x1b\x1b", []event{{key: keyCancel}, {key: keyCancel}}},
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
	m := &model{roots: roots, total: total, screens: map[string]screen{}}
	m.setQuery()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.query = []rune("xx src vim")[:1+i%10]
		m.setQuery()
		m.render(200, 60)
	}
}

func TestDecodeMouse(t *testing.T) {
	got := decode([]byte("\x1b[<0;5;3M\x1b[<0;5;3m\x1b[<65;1;1M\x1b[<0;5Mz"))
	want := []event{
		{key: keyMouse, button: 0, x: 4, y: 2, press: true},
		{key: keyMouse, button: 0, x: 4, y: 2},
		{key: keyMouse, button: 65, press: true},
		{key: keyRune, r: 'z'},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decode = %+v\nwant %+v", got, want)
	}
}

func TestMouse(t *testing.T) {
	// Pane contents are filled in so rendering never runs tmux.
	m := &model{roots: testTree(), total: 7, preview: true, screens: map[string]screen{
		"%1": {}, "%2": {}, "%3": {}, "%4": {},
	}}
	m.setQuery()
	m.render(80, 24) // tree on screen rows 2–10, preview on 11–22, footer on 23
	click := func(x, y int) (bool, *tmux.Target) {
		done, target, _ := m.handle(event{key: keyMouse, x: x, y: y, press: true})
		m.handle(event{key: keyMouse, x: x, y: y})
		return done, target
	}

	if done, _ := click(10, 4); done || m.selected().name != "server" {
		t.Fatalf("click should select server, got %q", m.selected().name)
	}
	m.handle(event{key: keyMouse, button: 64, press: true})
	if m.selected().name != "editor" {
		t.Errorf("wheel up should move to editor, got %q", m.selected().name)
	}
	if click(arrowColumn(m.rows[1].n), 3); len(m.rows) != 7 {
		t.Errorf("click on the arrow should expand editor, rows %q", names(m.rows))
	}
	m.lastClickRow = -1
	click(10, 5) // %2
	done, target := click(10, 5)
	if !done || target == nil || target.Pane != "%2" {
		t.Errorf("double click should jump to %%2, got %v %+v", done, target)
	}

	m.hits = []hit{{x0: 0, y0: 15, x1: 9, y1: 20, target: tmux.Target{Session: "$2", Window: "@3"}}}
	if done, target := click(5, 16); !done || target.Window != "@3" {
		t.Errorf("click in the preview should jump to its window, got %+v", target)
	}
	if click(5, 23); !m.help {
		t.Error("click on the footer should open help")
	}
	if click(5, 5); m.help || m.selected().pane == nil || m.selected().pane.ID != "%2" {
		t.Error("a click while help is open should only close it")
	}
}

func TestSort(t *testing.T) {
	pane := &tmux.Pane{ID: "%1"}
	session := func(id, name string, activity int64, windows ...*tmux.Window) *tmux.Session {
		return &tmux.Session{ID: id, Name: name, Activity: activity, Windows: windows}
	}
	window := func(index int, name string, activity int64) *tmux.Window {
		return &tmux.Window{ID: fmt.Sprint("@", index), Index: index, Name: name, Activity: activity, Panes: []*tmux.Pane{pane}}
	}
	// tmux lists sessions by name; ids give creation order.
	sessions := []*tmux.Session{
		session("$2", "alpha", 300),
		session("$10", "mid", 200),
		session("$1", "zeta", 100, window(0, "b", 5), window(1, "c", 9), window(2, "a", 1)),
	}
	roots, total := buildTree(sessions, tmux.Location{}, "")
	m := &model{roots: roots, total: total}
	m.setQuery()
	order := func() string {
		var s []string
		for _, r := range m.rows {
			s = append(s, r.n.name)
		}
		return strings.Join(s, " ")
	}
	tests := []struct {
		key  rune
		want string
	}{
		{'o', "alpha mid zeta a b c"}, // name
		{'O', "alpha mid zeta c b a"}, // time, most recent first
		{'r', "zeta a b c mid alpha"}, // time, reversed
		{'o', "mid alpha zeta a c b"}, // index, reversed: $10 $2 $1
		{'r', "zeta b c a alpha mid"}, // index: $1 $2 $10, windows by index
	}
	for _, tt := range tests {
		m.handle(alt(tt.key))
		if got := order(); got != tt.want {
			t.Errorf("after Alt-%c (sort %s, reverse %v): %q, want %q", tt.key, SortOrders[m.order], m.reverse, got, tt.want)
		}
	}
}

func TestSelectStart(t *testing.T) {
	m := &model{roots: testTree(), total: 7}
	m.setQuery()
	for _, r := range "blog" {
		m.handle(typed(r))
	}
	m.handle(alt('h'))
	if n := m.selected(); n.kind != paneRow || n.pane.ID != "%2" || len(m.query) != 0 {
		t.Errorf("Alt-h selected %+v with query %q; want pane %%2 and no query", n, string(m.query))
	}
	m.roots[0].children[0].expanded = false
	m.handle(event{key: keyEnd})
	m.handle(alt('H'))
	if n := m.selected(); n.kind != paneRow || n.pane.ID != "%2" {
		t.Errorf("Alt-H should expand the window and select %%2, got %+v", n)
	}
}

func TestExpandCollapseAll(t *testing.T) {
	m := &model{roots: testTree(), total: 7}
	m.setQuery()
	m.handle(event{key: keyEnd}) // posts
	m.handle(alt('-'))
	if got := strings.Join(names(m.rows), " "); got != "api blog" || m.selected().name != "blog" {
		t.Errorf("after Alt--: %q, selected %q", got, m.selected().name)
	}
	m.handle(alt('+'))
	if len(m.rows) != 7 || m.selected().name != "blog" {
		t.Errorf("after Alt-+: %q", names(m.rows))
	}
	m.handle(alt('-'))
	m.handle(alt('='))
	if len(m.rows) != 7 {
		t.Errorf("Alt-= should expand too: %q", names(m.rows))
	}
}

func TestShortcuts(t *testing.T) {
	m := &model{roots: testTree(), total: 7}
	m.setQuery()
	m.render(80, 24)
	b := &strings.Builder{}
	drawLine(b, 0, 80, "", false, m.rowSpans(m.rows[2].n, 2)...)
	if !strings.Contains(b.String(), "M-2 ") {
		t.Errorf("row 2 should be labelled M-2, got %q", b.String())
	}
	if done, target, _ := m.handle(alt('3')); !done || target.Session != "$2" || target.Window != "" {
		t.Errorf("Alt-3 should jump to blog, got %v %+v", done, target)
	}
	if done, _, _ := m.handle(alt('9')); done {
		t.Error("Alt-9 has no row and should do nothing")
	}
	for _, r := range "zsh" {
		m.handle(typed(r))
	}
	if _, target, _ := m.handle(alt('2')); target == nil || target.Pane != "%2" {
		t.Errorf("Alt-2 after filtering should jump to the first match, got %+v", target)
	}
}

func TestMenu(t *testing.T) {
	m := &model{roots: testTree(), total: 7}
	m.setQuery()
	m.render(80, 24)
	rightClick := func(x, y int) {
		m.handle(event{key: keyMouse, button: 2, x: x, y: y, press: true})
		m.handle(event{key: keyMouse, button: 2, x: x, y: y})
	}
	rightClick(10, 3) // editor
	if m.menu == nil || m.selected().name != "editor" {
		t.Fatalf("right-click should select editor and open the menu")
	}
	labels := ""
	for _, it := range m.menu.items {
		labels += it.label + "|"
	}
	if labels != "Select|Expand|Mark||Tag|Tag All|Tag None||Kill|Kill Tagged||Cancel|" {
		t.Errorf("items = %s", labels)
	}
	m.handle(event{key: keyDown})
	m.handle(event{key: keyEnter}) // Expand
	if m.menu != nil || len(m.rows) != 7 {
		t.Errorf("Expand from the menu: menu %v, rows %q", m.menu, names(m.rows))
	}

	rightClick(10, 3)
	m.handle(typed('t')) // tmux's letter for Tag
	if m.menu != nil || len(m.tags) != 1 {
		t.Errorf("t in the menu should tag, tags %v", m.tags)
	}

	rightClick(10, 2)
	var b strings.Builder
	m.menu.draw(&b, 80, 24)
	// Click the "Kill" item: the menu's first item row is one below its top.
	m.handle(event{key: keyMouse, x: m.menu.x + 2, y: m.menu.y + 1 + 8, press: true})
	if m.confirm == nil || !strings.Contains(m.confirm.question, "session api") {
		t.Errorf("clicking Kill should ask to kill api, got %+v", m.confirm)
	}
	m.handle(typed('n'))

	rightClick(10, 2)
	m.handle(typed('z'))
	if m.menu != nil || len(m.query) != 0 {
		t.Error("another key should close the menu without typing")
	}
}

func TestHelpScrolls(t *testing.T) {
	m := &model{roots: testTree(), total: 7}
	m.setQuery()
	m.handle(typed('?'))
	screen := m.render(80, 24)
	if !strings.Contains(screen, "to scroll (lines 1–20 of") {
		t.Fatalf("a 24-line screen should show a scroll hint")
	}
	m.handle(event{key: keyDown})
	m.handle(event{key: keyMouse, button: 65, press: true})
	m.render(80, 24)
	if !m.help || m.helpTop != 2 {
		t.Errorf("Down and the wheel should scroll the help, helpTop %d", m.helpTop)
	}
	m.handle(event{key: keyEnd})
	m.render(80, 24)
	if m.helpTop != len(helpText)-20 {
		t.Errorf("End should scroll to the last page, helpTop %d", m.helpTop)
	}
	m.handle(typed('a'))
	if m.help || string(m.query) != "a" {
		t.Errorf("another key should close the help and type, query %q", string(m.query))
	}
}

func TestNormalMode(t *testing.T) {
	if got := decode([]byte{0x1f, 0x04, 0x02, 0x06}); !reflect.DeepEqual(got,
		[]event{{key: keyToggleMode}, {key: keyHalfDown}, {key: keyPageUp}, {key: keyPageDown}}) {
		t.Errorf("decode = %v", got)
	}
	m := &model{roots: testTree(), total: 7}
	m.setQuery()
	m.render(80, 24)
	m.handle(event{key: keyToggleMode})
	if !m.normal {
		t.Fatal("Ctrl-/ should switch to normal mode")
	}
	m.handle(typed('j'))
	m.handle(typed('j'))
	m.handle(typed('k'))
	if m.selected().name != "editor" || len(m.query) != 0 {
		t.Errorf("j j k should select editor without typing, got %q, query %q", m.selected().name, string(m.query))
	}
	m.handle(typed('l'))
	if len(m.rows) != 7 {
		t.Errorf("l should expand editor, rows %q", names(m.rows))
	}
	m.handle(typed('t'))
	if len(m.tags) != 1 || m.selected().pane == nil {
		t.Errorf("t should tag editor and move down, tags %v", m.tags)
	}
	m.handle(typed('T'))
	m.handle(typed('G'))
	if len(m.tags) != 0 || m.selected().name != "posts" {
		t.Errorf("T should untag and G go last, tags %v, selected %q", m.tags, m.selected().name)
	}
	if done, target, _ := m.handle(typed('3')); !done || target.Pane != "%2" {
		t.Errorf("3 should jump to the fourth row, got %+v", target)
	}
	m.handle(typed('/'))
	m.handle(typed('z'))
	if m.normal || string(m.query) != "z" {
		t.Errorf("/ should return to search mode, query %q", string(m.query))
	}
	m.handle(event{key: keyToggleMode})
	if done, _, _ := m.handle(typed('q')); !done {
		t.Error("q should quit in normal mode")
	}
}

func TestNormalModeShortcutsAndSearch(t *testing.T) {
	var sessions []*tmux.Session
	for i := 0; i < 14; i++ {
		sessions = append(sessions, &tmux.Session{ID: fmt.Sprint("$", i), Name: fmt.Sprint("s", i)})
	}
	roots, total := buildTree(sessions, tmux.Location{}, "")
	m := &model{roots: roots, total: total, normal: true}
	m.setQuery()
	m.render(80, 30)
	if got := m.shortcutLabel(11); got != "M-b" {
		t.Errorf("label 11 = %q, want M-b", got)
	}
	if _, target, _ := m.handle(alt('b')); target == nil || target.Session != "$11" {
		t.Errorf("Alt-b should jump to row 11, got %+v", target)
	}
	m.normal = false
	if got := m.shortcutLabel(11); got != "" {
		t.Errorf("search mode has only ten shortcuts, label 11 = %q", got)
	}
	typeText(m, "s1")
	m.normal = true
	m.sel = 0
	m.handle(typed('n'))
	m.handle(typed('n'))
	if m.selected().name != "s11" {
		t.Errorf("n n from s1 should reach s11, got %q", m.selected().name)
	}
	for i := 0; i < 3; i++ {
		m.handle(typed('N'))
	}
	if m.selected().name != "s13" {
		t.Errorf("N should wrap backwards to s13, got %q", m.selected().name)
	}
}

func TestHelpSpans(t *testing.T) {
	text := func(s []span) (out string) {
		for _, x := range s {
			out += "[" + x.style + "]" + x.text
		}
		return out
	}
	tests := map[string]string{
		"# Mouse":              "[1;36]  Mouse",
		"| j k\tdown / up":     "[]    [33]j k[]" + strings.Repeat(" ", helpKeyWidth-3) + "down / up",
		"$ !zsh\trows":         "[]    [32]!zsh[]" + strings.Repeat(" ", helpKeyWidth-4) + "rows",
		"in NORMAL or SEARCH.": "[]  []in [1;7]NORMAL[] or [1;7]SEARCH[].",
		"":                     "[]  ",
	}
	for line, want := range tests {
		if got := text(helpSpans(line)); got != want {
			t.Errorf("helpSpans(%q) = %q, want %q", line, got, want)
		}
	}
}
