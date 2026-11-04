package tmux

import (
	"strings"
	"unicode/utf8"
)

// Key is one key for SendKeys: a tmux key name (Up, C-a, M-x, Enter), or
// with Literal, text typed as it is.
type Key struct {
	Name    string
	Literal bool
}

// SendKeys types keys into pane, in one tmux invocation.
func (t Tmux) SendKeys(pane string, keys []Key) error {
	var args []string
	for _, k := range keys {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(args, "send-keys", "-t", pane)
		if k.Literal {
			args = append(args, "-l", "--")
		}
		args = append(args, argument(k.Name))
	}
	if len(args) == 0 {
		return nil
	}
	_, err := t.Run(args...)
	return err
}

// argument protects an argument from tmux, which takes a ";" at the end of
// one for the end of a command, and "\;" there for a ";".
func argument(s string) string {
	if strings.HasSuffix(s, ";") {
		return s[:len(s)-1] + `\;`
	}
	return s
}

// Input turns bytes read from a terminal into the keys they are, named as
// tmux names them, so that SendKeys types them into a pane as the pane's
// program expects them (in its own cursor-key mode, say). Text is gathered
// into literal runs. Mouse reports, and sequences it does not know, are
// left out.
func Input(b []byte) []Key {
	var keys []Key
	text := func(s string) {
		if n := len(keys); n > 0 && keys[n-1].Literal {
			keys[n-1].Name += s
			return
		}
		keys = append(keys, Key{Name: s, Literal: true})
	}
	for len(b) > 0 {
		c := b[0]
		switch {
		case c == 0x1b:
			name, n := escapeKey(b)
			if name != "" {
				keys = append(keys, Key{Name: name})
			}
			b = b[n:]
		case c < 0x20 || c == 0x7f:
			keys = append(keys, Key{Name: controlKey(c)})
			b = b[1:]
		default:
			r, n := utf8.DecodeRune(b)
			if r != utf8.RuneError || n > 1 {
				text(string(r))
			}
			b = b[n:]
		}
	}
	return keys
}

// controlKey names a control character.
func controlKey(c byte) string {
	switch c {
	case 0:
		return "C-Space"
	case '\t':
		return "Tab"
	case '\r':
		return "Enter"
	case 0x1b:
		return "Escape"
	case 0x1c:
		return `C-\`
	case 0x1d:
		return "C-]"
	case 0x1e:
		return "C-^"
	case 0x1f:
		return "C-_"
	case 0x7f:
		return "BSpace"
	}
	return "C-" + string(rune('a'+c-1))
}

// csiNames names the final byte of an ESC [ or ESC O sequence.
var csiNames = map[byte]string{
	'A': "Up", 'B': "Down", 'C': "Right", 'D': "Left", 'H': "Home", 'F': "End",
	'P': "F1", 'Q': "F2", 'R': "F3", 'S': "F4",
}

// tildeNames names the first parameter of an ESC [ n ~ sequence.
var tildeNames = map[string]string{
	"1": "Home", "7": "Home", "4": "End", "8": "End", "2": "IC", "3": "DC",
	"5": "PPage", "6": "NPage",
	"11": "F1", "12": "F2", "13": "F3", "14": "F4", "15": "F5", "17": "F6",
	"18": "F7", "19": "F8", "20": "F9", "21": "F10", "23": "F11", "24": "F12",
}

// escapeKey names the key whose sequence starts b, which starts with ESC,
// or "" if it is not one; it returns how many bytes it took.
func escapeKey(b []byte) (string, int) {
	switch {
	case len(b) < 2:
		return "Escape", 1
	case b[1] == ' ':
		return "M-Space", 2
	case b[1] != '[' && b[1] != 'O':
		if b[1] > 0x20 && b[1] < 0x7f {
			return "M-" + string(b[1]), 2 // Alt and the key
		}
		return "Escape", 1
	case b[1] == 'O':
		if len(b) >= 3 && csiNames[b[2]] != "" {
			return csiNames[b[2]], 3
		}
		return "M-O", 2
	}
	for i := 2; i < len(b); i++ {
		if b[i] < 0x40 || b[i] > 0x7e {
			continue
		}
		params := string(b[2:i])
		if strings.HasPrefix(params, "<") {
			return "", i + 1 // a mouse report
		}
		first, mods, _ := strings.Cut(params, ";")
		name := ""
		switch {
		case b[i] == '~':
			name = tildeNames[first]
		case b[i] == 'Z':
			name = "BTab"
		default:
			name = csiNames[b[i]]
		}
		if name == "" {
			return "", i + 1
		}
		return modifiers(mods) + name, i + 1
	}
	return "", len(b)
}

// modifiers turns the modifier parameter of a key sequence (1 + shift 1,
// alt 2, ctrl 4) into tmux's prefixes.
func modifiers(p string) string {
	n := 0
	for _, c := range p {
		if c < '0' || c > '9' {
			return ""
		}
		n = n*10 + int(c-'0')
	}
	if n < 2 {
		return ""
	}
	n--
	s := ""
	if n&4 != 0 {
		s += "C-"
	}
	if n&2 != 0 {
		s += "M-"
	}
	if n&1 != 0 {
		s += "S-"
	}
	return s
}
