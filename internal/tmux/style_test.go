package tmux

import "testing"

func TestStyleApply(t *testing.T) {
	for _, tt := range []struct{ spec, want string }{
		{"fg=red,bold", "1;31"},
		{"bg=green fg=black", "30;42"},
		{"fg=colour99,bg=brightblue", "38;5;99;104"},
		{"fg=#ff8000", "38;2;255;128;0"},
		{"fg=purple", "38;2;160;32;240"},
		{"fg=SteelBlue", "38;2;70;130;180"},
		{"reverse,noreverse,italics", "3"},
		{"bold,none", ""},
		{"fg=red,default", "7"}, // default goes back to the default given
		{"align=left range=window|1 push-default", ""},
		{"fg=nosuchcolour", ""},
	} {
		if got := (Style{}).Apply(tt.spec, Style{Attrs: attrBit("reverse")}).SGR(); got != tt.want {
			t.Errorf("Apply(%q) = %q, want %q", tt.spec, got, tt.want)
		}
	}
}

func TestTabStyle(t *testing.T) {
	for _, tt := range []struct {
		status, tab, text string
		want              string
		stands            bool
	}{
		{"bg=green,fg=black", "default", "1:bash", "30;42", false},
		{"bg=green,fg=black", "default reverse", "1:bash", "7;30;42", true},
		{"bg=green,fg=black", "default", "#[fg=purple,bold]4:editor#[default]", "1;38;2;160;32;240;42", true},
		// The most of the tab: the name, not a separator.
		{"", "default", "#[fg=red]>#[default]long name", "", false},
		{"", "bg=blue", "", "44", true},
	} {
		s, stands := TabStyle(tt.status, tt.tab, tt.text)
		if s.SGR() != tt.want || stands != tt.stands {
			t.Errorf("TabStyle(%q, %q, %q) = %q, %v; want %q, %v", tt.status, tt.tab, tt.text, s.SGR(), stands, tt.want, tt.stands)
		}
	}
}
