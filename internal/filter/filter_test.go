package filter

import (
	"reflect"
	"testing"
)

// vimPane is a pane running vim in window "editor" of session "api".
var vimPane = []Field{
	{Session, "api"},
	{Window, "editor"},
	{Command, "nvim"},
	{Path, "~/www/api-server"},
	{Title, "host"},
}

func TestMatch(t *testing.T) {
	tests := []struct {
		query string
		mode  Mode
		want  bool
	}{
		{"", Words, true},
		{"vim", Words, true},
		{"api vim", Words, true}, // terms found in different fields
		{"api emacs", Words, false},
		{"VIM", Words, false}, // uppercase turns off case folding
		{"API", Words, false},
		{"Api", Words, false},
		{"!emacs", Words, true},
		{"!vim", Words, false},
		{"api !c:nvim", Words, false},
		{"s:api", Words, true},
		{"s:editor", Words, false},
		{"w:edit", Words, true},
		{"c:vim", Words, true},
		{"c:api", Words, false},
		{"d:www", Words, true},
		{"d:nvim", Words, false},
		{"s:", Words, false}, // a bare prefix is a plain word
		{"!", Words, false},
		{"api-server", Words, true},
		{".", Words, false}, // no regex characters in words mode
		{"^api$", Regex, true},
		{"^edit$", Regex, false},
		{"/^n?vim$/", Words, true}, // /…/ switches to regex
		{"^API$", Regex, false},
		{"(?i)^API$", Regex, true},
		{`\Snvim`, Regex, false},
		{`\snvim|^nvim`, Regex, true}, // \s is not an uppercase letter
		{"//", Words, true},
	}
	for _, tt := range tests {
		m, err := New(tt.query, tt.mode)
		if err != nil {
			t.Errorf("New(%q): %v", tt.query, err)
			continue
		}
		if got := m.Match(vimPane); got != tt.want {
			t.Errorf("New(%q, %v).Match = %v, want %v", tt.query, tt.mode, got, tt.want)
		}
	}
}

func TestSmartCaseRegexEscapes(t *testing.T) {
	m, err := New(`\S+vim`, Regex)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Match([]Field{{Command, "NVIM"}}) {
		t.Error(`\S should not turn off case folding`)
	}
}

func TestInvalidRegex(t *testing.T) {
	if _, err := New("po(st", Regex); err == nil {
		t.Error("want an error for an unclosed group")
	}
	if _, err := New("po(st", Words); err != nil {
		t.Errorf("words mode should take ( literally: %v", err)
	}
}

func TestMode(t *testing.T) {
	m, _ := New("/x/", Words)
	if m.Mode() != Regex {
		t.Errorf("Mode = %v, want regex", m.Mode())
	}
	m, _ = New("/x", Words)
	if m.Mode() != Words {
		t.Errorf("Mode = %v, want words", m.Mode())
	}
}

func TestHighlight(t *testing.T) {
	tests := []struct {
		query string
		mode  Mode
		kind  Kind
		s     string
		want  [][2]int
	}{
		{"pi", Words, Session, "api pipe", [][2]int{{1, 3}, {4, 6}}},
		{"PI", Words, Session, "api PIpe", [][2]int{{4, 6}}},
		{"s:pi", Words, Command, "api", nil},
		{"!pi", Words, Session, "api", nil},
		{"p+", Regex, Session, "apppi", [][2]int{{1, 4}}},
	}
	for _, tt := range tests {
		m, _ := New(tt.query, tt.mode)
		if got := m.Highlight(tt.kind, tt.s); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Highlight(%q in %q) = %v, want %v", tt.query, tt.s, got, tt.want)
		}
	}
}
