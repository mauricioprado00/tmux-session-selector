package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mauricioprado00/tmux-session-selector/internal/filter"
	"github.com/mauricioprado00/tmux-session-selector/internal/jobs"
	"github.com/mauricioprado00/tmux-session-selector/internal/procinfo"
	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

func TestDecodeCtrlZ(t *testing.T) {
	if got := decode([]byte{0x1a}); !reflect.DeepEqual(got, []event{{key: keyToggleJobs}}) {
		t.Errorf("decode(Ctrl-Z) = %v", got)
	}
}

// jobsTree is testTree with shell pids: %2 (a pane of the two-pane editor
// window) has a stopped vim, and %3 (the single pane of the server window)
// has a background make that started a compiler.
func jobsTree() ([]*node, map[int][]*jobs.Job) {
	roots := testTree()
	pids := map[string]int{"%1": 10, "%2": 20, "%3": 30, "%4": 40}
	walkTree(roots, func(n *node) {
		if n.pane != nil {
			n.pane.PID = pids[n.pane.ID]
		}
	})
	return roots, map[int][]*jobs.Job{
		20: {{PID: 201, Stopped: true, Args: "vim notes.md"}},
		30: {{PID: 301, Args: "make -j4", Children: []*jobs.Job{{PID: 302, Args: "cc main.c"}}}},
	}
}

func TestJobRows(t *testing.T) {
	roots, found := jobsTree()
	if got := panePIDs(roots); !reflect.DeepEqual(got, []int{10, 20, 30, 40}) {
		t.Errorf("panePIDs = %v", got)
	}
	m := &model{roots: roots, total: 7}
	m.setQuery()
	m.jobCount = attachJobs(roots, found, nil)
	m.total += m.jobCount
	m.refresh()
	if m.jobCount != 3 {
		t.Errorf("attached %d job rows, want 3", m.jobCount)
	}
	// The editor window, collapsed at start, opens to show the job.
	want := []string{"api", "editor", "%1", "%2", "vim notes.md", "server", "make -j4", "cc main.c", "blog", "posts"}
	if got := jobNames(m.rows); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}

	vim := m.rows[4].n
	if got := vim.target(); got != (tmux.Target{Session: "$1", Window: "@1", Pane: "%2"}) {
		t.Errorf("job target = %+v, want its pane", got)
	}
	if got := arrowColumn(m.rows[7].n); got != gutterWidth+1+2*3 {
		t.Errorf("arrow column of a job of a job = %d", got)
	}

	// Searching finds jobs by command, with their pane as context.
	mt, _ := filter.New("c:cc", filter.Words)
	rows, matches := visible(roots, mt)
	if got := jobNames(rows); matches != 1 || !reflect.DeepEqual(got, []string{"(api)", "(server)", "(make -j4)", "cc main.c"}) {
		t.Errorf("search for a job = %q, %d", got, matches)
	}

	// Killing or tagging a job row must not touch its pane.
	m.sel = 4
	m.command('x')
	m.command('t')
	if m.confirm != nil || len(m.tags) != 0 || !strings.Contains(m.status, "background process") {
		t.Errorf("x or t on a job row: confirm=%v tags=%v status=%q", m.confirm, m.tags, m.status)
	}
	m.handle(event{key: keyTagAll})
	if len(m.tags) != 7 {
		t.Errorf("tag all tagged %d rows, want the 7 tmux rows", len(m.tags))
	}

	detachJobs(roots)
	m.refresh()
	if got := jobNames(m.rows); !reflect.DeepEqual(got, []string{"api", "editor", "%1", "%2", "server", "blog", "posts"}) {
		t.Errorf("after hiding jobs rows = %q", got)
	}
	if m.selected() != roots[0].children[0].children[1] {
		t.Errorf("selection on a hidden job should move to its pane, got %+v", m.selected())
	}
}

// jobNames is names, with job rows shown by their command.
func jobNames(rows []row) []string {
	out := names(rows)
	for i, r := range rows {
		if r.n.kind == jobRow {
			out[i] = r.n.command
			if r.context {
				out[i] = "(" + out[i] + ")"
			}
		}
	}
	return out
}

func TestJobRowContent(t *testing.T) {
	roots, found := jobsTree()
	attachJobs(roots, found, nil)
	m := &model{roots: roots}
	m.setQuery()
	text := func(n *node) string {
		var b strings.Builder
		for _, s := range m.rowContent(n) {
			b.WriteString(s.text)
		}
		return b.String()
	}
	editor := roots[0].children[0]
	pane, vim := editor.children[1], editor.children[1].children[0]
	if got := text(pane); !strings.HasPrefix(got, "*    ▾ %2  zsh") {
		t.Errorf("pane with a job = %q", got)
	}
	if got := text(vim); got != "         201 vim notes.md  stopped" {
		t.Errorf("job row = %q", got)
	}
}

func TestClaudeTitle(t *testing.T) {
	tests := []struct {
		command, title, want string
	}{
		{"claude", "✳ Fix the login form", "✳ Fix the login form"},
		{"node", "⠐ Fix the login form", "⠐ Fix the login form"}, // working, run through node
		{"bash", "✳ Fix the login form", ""},                     // Claude Code has exited
		{"claude", "myhost", ""},                                 // not set by Claude Code yet
		{"vim", "notes.md", ""},
		{"claude", "✳", ""},
	}
	for _, tt := range tests {
		if got := claudeTitle(&tmux.Pane{Command: tt.command, Title: tt.title}); got != tt.want {
			t.Errorf("claudeTitle(%q, %q) = %q, want %q", tt.command, tt.title, got, tt.want)
		}
	}
}

func TestClaudeTitleShown(t *testing.T) {
	sessions := []*tmux.Session{{ID: "$1", Name: "work", Windows: []*tmux.Window{
		{ID: "@1", Name: "ai", Panes: []*tmux.Pane{{ID: "%1", Command: "claude", Title: "✳ Fix the login form", Path: "/src"}}},
	}}}
	roots, _ := buildTree(sessions, tmux.Location{}, "")
	m := &model{roots: roots, titles: true}
	m.query = []rune("login")
	m.setQuery()
	if len(m.rows) != 2 || m.rows[1].context {
		t.Fatalf("the title should be searchable: %q", names(m.rows))
	}
	var title span
	for _, s := range m.rowContent(roots[0].children[0]) {
		if strings.Contains(s.text, "Fix the login form") {
			title = s
		}
	}
	if title.style != styleTitle || len(title.hl) == 0 {
		t.Errorf("title span = %+v, want styled and highlighted", title)
	}
}

func TestPreviewSetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state")
	if got := readState(path); len(got) != 0 {
		t.Errorf("missing file read as %v", got)
	}
	// Nothing saved yet: the terminal's height decides.
	if initialPreview(false, readState(path)["preview"], 40) != true || initialPreview(false, "", 20) != false {
		t.Error("with nothing saved, the preview should follow the height")
	}
	m := &model{roots: testTree(), total: 7, stateFile: path, preview: true}
	m.setQuery()
	m.handle(event{key: keyTogglePreview})
	if got := readState(path)["preview"]; got != "off" || m.status != "" {
		t.Fatalf("after hiding, saved preview = %q (status %q)", got, m.status)
	}
	if initialPreview(false, readState(path)["preview"], 100) {
		t.Error("hidden last time, the preview should start hidden even on a tall terminal")
	}
	if err := saveSetting(path, "other", "x"); err != nil {
		t.Fatal(err)
	}
	m.handle(event{key: keyTogglePreview})
	if got := readState(path); got["preview"] != "on" || got["other"] != "x" {
		t.Errorf("after showing, state = %v", got)
	}
	if !initialPreview(false, "on", 5) || initialPreview(true, "on", 100) {
		t.Error("shown last time it starts shown, unless -no-preview")
	}
	// Without a state file nothing is written and nothing fails.
	m.stateFile = ""
	m.handle(event{key: keyTogglePreview})
	if m.status != "" {
		t.Errorf("status = %q", m.status)
	}
}

func TestClaudeTitlesInOverview(t *testing.T) {
	// An 80x24 window: a shell on the left, Claude Code on the right, and
	// Claude Code again across the bottom.
	w := &tmux.Window{Width: 80, Height: 24, Panes: []*tmux.Pane{
		{ID: "%1", Command: "bash", Title: "host", Width: 39, Height: 15},
		{ID: "%2", Command: "claude", Title: "✳ Fix login", Left: 40, Width: 40, Height: 15},
		{ID: "%3", Command: "claude", Title: "✳ Write docs", Top: 16, Width: 80, Height: 8},
	}}
	c := newCanvas(82, 26)
	label := " 1: ai "
	c.text(2, 0, 80, label, styleLabel)
	(&model{}).drawPaneTitles(c, w, 0, 0, 81, 25, 2+textWidth(label))
	row := func(y int) string {
		var b strings.Builder
		for _, cl := range c.cells[y] {
			if cl.r != 0 {
				b.WriteRune(cl.r)
			}
		}
		return b.String()
	}
	if top := row(0); !strings.Contains(top, " 1: ai ") || !strings.Contains(top, " ✳ Fix login ") ||
		utf8.RuneCountInString(top[:strings.Index(top, "Fix login")]) < 42 {
		t.Errorf("top border = %q, want the right pane's title on its part of the border", top)
	}
	if mid := row(16); !strings.Contains(mid, " ✳ Write docs ") {
		t.Errorf("bottom pane's border = %q, want its title", mid)
	}
	for _, y := range []int{0, 16} {
		for _, cl := range c.cells[y] {
			if cl.r == '✳' && cl.style != styleTitle {
				t.Errorf("title on row %d styled %q", y, cl.style)
			}
		}
	}
}

func TestToggleTitles(t *testing.T) {
	if got := decode([]byte{0x0c}); !reflect.DeepEqual(got, []event{{key: keyToggleTitles}}) {
		t.Errorf("decode(Ctrl-L) = %v", got)
	}
	sessions := []*tmux.Session{{ID: "$1", Name: "work", Windows: []*tmux.Window{
		{ID: "@1", Name: "ai", Width: 80, Height: 24, Active: true, Panes: []*tmux.Pane{
			{ID: "%1", Command: "claude", Title: "✳ Fix the login form", Width: 80, Height: 24, Active: true}}},
	}}}
	roots, total := buildTree(sessions, tmux.Location{}, "")
	path := filepath.Join(t.TempDir(), "state")
	m := &model{roots: roots, total: total, titles: true, stateFile: path, screens: map[string]screen{"%1": {}}}
	m.setQuery()
	shows := func() (list, overview bool) {
		var b strings.Builder
		for _, s := range m.rowContent(roots[0].children[0]) {
			b.WriteString(s.text)
		}
		m.openOverview()
		defer func() { m.overview = nil }()
		return strings.Contains(b.String(), "Fix the login"), strings.Contains(m.render(100, 30), "Fix the login")
	}
	if l, o := shows(); !l || !o {
		t.Fatalf("titles on: list %v, overview %v", l, o)
	}
	m.handle(event{key: keyToggleTitles}) // in the list
	if l, o := shows(); l || o || readState(path)["claude-titles"] != "off" {
		t.Errorf("after Ctrl-L in the list: list %v, overview %v, saved %q", l, o, readState(path)["claude-titles"])
	}
	m.openOverview()
	m.handle(event{key: keyToggleTitles}) // in the overview
	m.overview = nil
	if l, o := shows(); !l || !o || readState(path)["claude-titles"] != "on" {
		t.Errorf("after Ctrl-L in the overview: list %v, overview %v, saved %q", l, o, readState(path)["claude-titles"])
	}
}

func TestStoppedClaudeJobTitle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sessions", "201.json"), `{"pid":201,"sessionId":"s1","cwd":"/src"}`)
	writeFile(t, filepath.Join(dir, "projects", "-src", "s1.jsonl"),
		`{"type":"ai-title","aiTitle":"Fix the login form","sessionId":"s1"}`+"\n")
	roots, _ := jobsTree()
	m := &model{roots: roots, titles: true, claudeDirs: []string{dir}}
	m.setQuery()
	found := map[int][]*jobs.Job{20: {{PID: 201, Stopped: true, Args: "claude --resume s1"}, {PID: 202, Args: "make"}}}
	attachJobs(roots, found, func(j *jobs.Job) string {
		if isClaude(j.Args) {
			return "✳ " + procinfo.ClaudeTitle(m.claudeDirs[0], j.PID)
		}
		return ""
	})
	pane := roots[0].children[0].children[1]
	text := func(n *node) string {
		var b strings.Builder
		for _, s := range m.rowContent(n) {
			b.WriteString(s.text)
		}
		return b.String()
	}
	if got := text(pane.children[0]); !strings.HasSuffix(got, "claude --resume s1  ✳ Fix the login form  stopped") {
		t.Errorf("claude job row = %q", got)
	}
	if got := text(pane.children[1]); strings.Contains(got, "✳") {
		t.Errorf("make has no title: %q", got)
	}
	m.titles = false
	if got := text(pane.children[0]); strings.Contains(got, "Fix the login") {
		t.Errorf("Ctrl-L off should hide the job's title: %q", got)
	}
	// The title is searchable.
	mt, _ := filter.New("login", filter.Words)
	if rows, n := visible(roots, mt); n != 1 || rows[len(rows)-1].n != pane.children[0] {
		t.Errorf("search for the title found %d rows", n)
	}
}

func TestEditorFilesShown(t *testing.T) {
	proc := t.TempDir()
	writeFile(t, filepath.Join(proc, "10", "stat"), "10 (bash) S 1 10 10 34817 30 0")
	writeFile(t, filepath.Join(proc, "30", "cmdline"), "vim\x00notes.md\x00")
	ws := windows(2)
	ws[0].Panes[0].Command, ws[0].Panes[0].PID = "vim", 10
	s := &tmux.Session{ID: "$1", Name: "work", Windows: ws}
	fake := &model{proc: proc}
	if got, style := fake.paneTitle(ws[0].Panes[0]); got != "notes.md" || style != styleFile {
		t.Fatalf("paneTitle = %q %q", got, style)
	}
	roots, total := buildTree([]*tmux.Session{s}, tmux.Location{}, "")
	m := &model{roots: roots, total: total, proc: proc, titles: true, screens: map[string]screen{}}
	m.setQuery()
	row := func() string {
		var b strings.Builder
		for _, sp := range m.rowContent(roots[0].children[0]) {
			b.WriteString(sp.text)
		}
		return b.String()
	}
	if !strings.Contains(row(), "vim  notes.md  ") {
		t.Errorf("list row = %q", row())
	}
	m.handle(event{key: keyOverview})
	if !strings.Contains(m.render(120, 40), " notes.md ") {
		t.Error("the overview should show the file on the pane's border")
	}
	m.handle(event{key: keyToggleTitles})
	if strings.Contains(m.render(120, 40), "notes.md") || strings.Contains(row(), "notes.md") {
		t.Error("Ctrl-L should hide the file in both")
	}
	// The overview's search finds it.
	for _, r := range "/notes" {
		m.handle(event{key: keyRune, r: r})
	}
	if m.matchCount() != 1 {
		t.Errorf("search for the file matched %d windows", m.matchCount())
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestJobsSetting(t *testing.T) {
	s := &tmux.Session{ID: "$1", Name: "work", Windows: windows(1)}
	roots, total := buildTree([]*tmux.Session{s}, tmux.Location{}, "")
	path := filepath.Join(t.TempDir(), "state")
	m := &model{roots: roots, total: total, stateFile: path, screens: map[string]screen{}}
	m.setQuery()
	m.handle(event{key: keyToggleJobs})
	if !m.jobs || readState(path)["background-jobs"] != "on" {
		t.Errorf("after Ctrl-Z: shown %v, saved %q", m.jobs, readState(path)["background-jobs"])
	}
	m.handle(event{key: keyToggleJobs})
	if m.jobs || readState(path)["background-jobs"] != "off" {
		t.Errorf("after a second Ctrl-Z: shown %v, saved %q", m.jobs, readState(path)["background-jobs"])
	}
}

func TestTogglesShownOn(t *testing.T) {
	on := "\x1b[0;" + styleOn + "m"
	s := &tmux.Session{ID: "$1", Name: "work", Windows: windows(2)}
	roots, total := buildTree([]*tmux.Session{s}, tmux.Location{}, "")
	m := &model{roots: roots, total: total, stateFile: filepath.Join(t.TempDir(), "state"), screens: map[string]screen{}}
	m.setQuery()
	if out := m.render(200, 30); strings.Contains(out, on) {
		t.Error("nothing is on: no hint should be in styleOn")
	}
	m.handle(event{key: keyToggleJobs})
	m.titles = true
	out := m.render(200, 30)
	if !strings.Contains(out, on+"^Z bg jobs") || !strings.Contains(out, on+"^L titles") ||
		strings.Contains(out, on+"^V preview") {
		t.Errorf("footer with bg jobs and titles on: %q", out)
	}
	m.help = true
	if out := m.render(200, 80); !strings.Contains(out, on+"Ctrl-Z") || strings.Contains(out, on+"Ctrl-V") {
		t.Errorf("help with bg jobs on: %q", out)
	}
	m.help = false
	m.dirs = true
	m.openOverview()
	if out := m.render(200, 30); !strings.Contains(out, on+"d dirs") || strings.Contains(out, on+"o centre") {
		t.Errorf("overview footer with dirs on: %q", out)
	}
}
