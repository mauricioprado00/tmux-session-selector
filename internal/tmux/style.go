package tmux

import (
	_ "embed"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// A window's tab, in the status line, has the colours tmux gives it: the
// status line's style, then window-status-style (or -current-style for the
// current window), -last-style, -bell-style or -activity-style, as its
// flags have it, and last the #[...] styles in window-status-format (or
// -current-format). These are tmux's default status-format; tabFields asks
// tmux for the parts and TabStyle puts them together.

// tabRange is the style tmux's default status-format gives a window's tab.
const tabRange = "#{?window_active," +
	"#{?#{!=:#{E:window-status-current-style},default},#{E:window-status-current-style},#{E:window-status-style}}," +
	"#{E:window-status-style}}" +
	"#{?#{&&:#{window_last_flag},#{!=:#{E:window-status-last-style},default}}, #{E:window-status-last-style},}" +
	"#{?#{&&:#{window_bell_flag},#{!=:#{E:window-status-bell-style},default}}, #{E:window-status-bell-style}," +
	"#{?#{&&:#{||:#{window_activity_flag},#{window_silence_flag}},#{!=:#{E:window-status-activity-style},default}}, #{E:window-status-activity-style},}}"

// tabText is a window's tab as tmux writes it, with its #[...] styles.
const tabText = "#{?window_active,#{T:window-status-current-format},#{T:window-status-format}}"

// tabFields are the formats, in paneFields, that make a window's tab style:
// whether there is a status line, its style, the tab's style and its text.
var tabFields = []string{"#{status}", "#{E:status-style}", tabRange, tabText}

// Style is a tmux style as SGR parameters: the colours, and attributes.
type Style struct {
	FG, BG string // "" is the terminal's own
	Attrs  uint16
}

// Attributes, as tmux names them, and their SGR parameter.
var attrNames = []struct {
	name string
	sgr  string
}{
	{"bright", "1"}, {"dim", "2"}, {"italics", "3"}, {"underscore", "4"}, {"blink", "5"},
	{"reverse", "7"}, {"hidden", "8"}, {"strikethrough", "9"}, {"overline", "53"},
	{"double-underscore", "4"}, {"curly-underscore", "4"}, {"dotted-underscore", "4"}, {"dashed-underscore", "4"},
}

func attrBit(name string) uint16 {
	if name == "bold" {
		name = "bright"
	}
	for i, a := range attrNames {
		if a.name == name {
			return 1 << i
		}
	}
	return 0
}

// SGR is the style as SGR parameters, such as "1;38;5;99;42"; "" is no style.
func (s Style) SGR() string {
	var p []string
	seen := map[string]bool{}
	for i, a := range attrNames {
		if s.Attrs&(1<<i) != 0 && !seen[a.sgr] {
			seen[a.sgr] = true
			p = append(p, a.sgr)
		}
	}
	for _, c := range []string{s.FG, s.BG} {
		if c != "" {
			p = append(p, c)
		}
	}
	return strings.Join(p, ";")
}

// Apply changes s by a tmux style such as "fg=red,bold" or "default"
// (which goes back to def), as tmux does; parts that are not about colours
// or attributes (align=, range=, push-default ...) are left out.
func (s Style) Apply(spec string, def Style) Style {
	for _, part := range strings.FieldsFunc(spec, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		part = strings.ToLower(part)
		key, value, isKey := strings.Cut(part, "=")
		switch {
		case isKey && key == "fg":
			if c, ok := colour(value, 30); ok {
				s.FG = c
			}
		case isKey && key == "bg":
			if c, ok := colour(value, 40); ok {
				s.BG = c
			}
		case isKey:
		case part == "default":
			s = def
		case part == "none":
			s.Attrs = 0
		case strings.HasPrefix(part, "no") && attrBit(part[2:]) != 0:
			s.Attrs &^= attrBit(part[2:])
		default:
			s.Attrs |= attrBit(part)
		}
	}
	return s
}

// colourNames are the eight colours by name and number, as tmux takes them.
var colourNames = []string{"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"}

// colour turns a tmux colour into its SGR parameter, base 30 for the
// foreground or 40 for the background.
func colour(v string, base int) (string, bool) {
	ext := strconv.Itoa(base + 8) // 38 or 48
	switch {
	case v == "default" || v == "terminal":
		return "", true
	case strings.HasPrefix(v, "#") && len(v) == 7:
		n, err := strconv.ParseUint(v[1:], 16, 32)
		if err != nil {
			return "", false
		}
		return rgb(ext, n), true
	case strings.HasPrefix(v, "colour") || strings.HasPrefix(v, "color"):
		n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimPrefix(v, "colour"), "color"))
		if err != nil || n < 0 || n > 255 {
			return "", false
		}
		return ext + ";5;" + strconv.Itoa(n), true
	}
	for i, name := range colourNames {
		switch v {
		case name, strconv.Itoa(i):
			return strconv.Itoa(base + i), true
		case "bright" + name, strconv.Itoa(90 + i):
			return strconv.Itoa(base + 60 + i), true
		}
	}
	if n, ok := x11Colour(strings.ReplaceAll(v, " ", "")); ok {
		return rgb(ext, n), true
	}
	return "", false
}

func rgb(ext string, n uint64) string {
	return ext + ";2;" + strconv.Itoa(int(n>>16&0xff)) + ";" + strconv.Itoa(int(n>>8&0xff)) + ";" + strconv.Itoa(int(n&0xff))
}

// colours.txt holds the X11 colour names tmux knows, such as purple or
// SteelBlue, lowercased and without spaces, one "name rrggbb" a line.
//
//go:embed colours.txt
var x11Text string

var x11 = sync.OnceValue(func() map[string]uint64 {
	m := map[string]uint64{}
	for _, line := range strings.Split(x11Text, "\n") {
		if name, hex, ok := strings.Cut(line, " "); ok {
			if n, err := strconv.ParseUint(hex, 16, 32); err == nil {
				m[name] = n
			}
		}
	}
	return m
})

func x11Colour(name string) (uint64, bool) {
	n, ok := x11()[name]
	return n, ok
}

// TabStyle is the style of the most of a window's tab, given the status
// line's style, the tab's and the tab's text with its #[...] styles, and
// whether it stands out from the status line, where it would be drawn.
func TabStyle(status, tab, text string) (Style, bool) {
	base := Style{}.Apply(status, Style{})
	def := base.Apply(tab, base)
	cur, most := def, def
	count := map[Style]int{}
	for text != "" {
		if strings.HasPrefix(text, "#[") {
			if end := strings.IndexByte(text, ']'); end > 0 {
				cur = cur.Apply(text[2:end], def)
				text = text[end+1:]
				continue
			}
		}
		_, n := utf8.DecodeRuneInString(text)
		text = text[n:]
		count[cur]++
		if count[cur] > count[most] {
			most = cur
		}
	}
	return most, most != base
}
