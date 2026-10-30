// Package filter parses a query and matches it against the rows of the tree.
// It does no I/O.
package filter

import (
	"regexp"
	"strings"
	"unicode"
)

// Kind says which tmux field a value comes from.
type Kind int

const (
	Session Kind = iota
	Window
	Index
	Command
	Path
	Title
)

// Field is one searchable value of a row.
type Field struct {
	Kind  Kind
	Value string
}

type Mode int

const (
	Words Mode = iota
	Regex
)

func (m Mode) String() string {
	if m == Regex {
		return "regex"
	}
	return "words"
}

// prefixes restrict a words-mode term to some fields.
var prefixes = map[string][]Kind{
	"s:": {Session},
	"w:": {Window, Index},
	"c:": {Command},
	"d:": {Path},
}

type term struct {
	text  string
	neg   bool
	kinds []Kind // nil means any field
}

func (t term) allows(k Kind) bool {
	if t.kinds == nil {
		return true
	}
	for _, kk := range t.kinds {
		if kk == k {
			return true
		}
	}
	return false
}

// Matcher is a parsed query.
type Matcher struct {
	mode  Mode
	fold  bool
	terms []term
	re    *regexp.Regexp
}

// New parses query. In Words mode, a query written as /…/ is taken as a regex.
// The error is set only for an invalid regex.
func New(query string, mode Mode) (*Matcher, error) {
	slashed := len(query) >= 2 && query[0] == '/' && query[len(query)-1] == '/'
	if slashed {
		mode = Regex
	}
	m := &Matcher{mode: mode, fold: !hasUpper(query, mode == Regex)}
	if mode == Regex {
		expr := query
		if slashed {
			expr = expr[1 : len(expr)-1]
		}
		if expr == "" {
			return m, nil
		}
		// Compile once as typed so an error message shows the user's text,
		// not the added (?i).
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, err
		}
		if m.fold {
			re = regexp.MustCompile("(?i)" + expr)
		}
		m.re = re
		return m, nil
	}
	for _, tok := range strings.Fields(query) {
		var t term
		if len(tok) > 1 && tok[0] == '!' {
			t.neg = true
			tok = tok[1:]
		}
		if len(tok) > 2 {
			if kinds, ok := prefixes[tok[:2]]; ok {
				t.kinds = kinds
				tok = tok[2:]
			}
		}
		t.text = m.norm(tok)
		m.terms = append(m.terms, t)
	}
	return m, nil
}

// Mode is the mode the query was parsed in, which is Regex for a /…/ query
// even when Words was asked for.
func (m *Matcher) Mode() Mode { return m.mode }

// Empty reports whether the query matches everything.
func (m *Matcher) Empty() bool { return m.re == nil && len(m.terms) == 0 }

// Match reports whether a row with these fields matches. In words mode every
// term must be found in some field it allows; in regex mode the expression
// must match some field.
func (m *Matcher) Match(fields []Field) bool {
	if m.re != nil {
		for _, f := range fields {
			if m.re.MatchString(f.Value) {
				return true
			}
		}
		return false
	}
	for _, t := range m.terms {
		if m.found(t, fields) == t.neg {
			return false
		}
	}
	return true
}

func (m *Matcher) found(t term, fields []Field) bool {
	for _, f := range fields {
		if t.allows(f.Kind) && strings.Contains(m.norm(f.Value), t.text) {
			return true
		}
	}
	return false
}

// Highlight returns the byte ranges of s, a value of the given kind, that the
// query matched. Ranges may overlap.
func (m *Matcher) Highlight(kind Kind, s string) [][2]int {
	var out [][2]int
	if m.re != nil {
		for _, loc := range m.re.FindAllStringIndex(s, -1) {
			if loc[0] < loc[1] {
				out = append(out, [2]int{loc[0], loc[1]})
			}
		}
		return out
	}
	hay := m.norm(s)
	if len(hay) != len(s) {
		return nil // case folding changed byte offsets
	}
	for _, t := range m.terms {
		if t.neg || t.text == "" || !t.allows(kind) {
			continue
		}
		for i := 0; ; {
			j := strings.Index(hay[i:], t.text)
			if j < 0 {
				break
			}
			out = append(out, [2]int{i + j, i + j + len(t.text)})
			i += j + len(t.text)
		}
	}
	return out
}

func (m *Matcher) norm(s string) string {
	if m.fold {
		return strings.ToLower(s)
	}
	return s
}

// hasUpper reports whether s has an uppercase letter, which turns off case
// folding. In a regex, escapes such as \S or \W do not count.
func hasUpper(s string, regex bool) bool {
	escaped := false
	for _, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if regex && r == '\\' {
			escaped = true
			continue
		}
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}
