package ui

import "unicode/utf8"

type key int

const (
	keyRune key = iota
	keyEnter
	keyBackspace
	keyDeleteWord
	keyClear
	keyUp
	keyDown
	keyPageUp
	keyPageDown
	keyHome
	keyEnd
	keyExpand
	keyCollapse
	keyToggleRegex
	keyTogglePreview
	keyCancel
)

type event struct {
	key key
	r   rune // for keyRune
}

var ctrlKeys = map[byte]key{
	'\r': keyEnter,
	0x08: keyBackspace, // Ctrl-H
	0x7f: keyBackspace,
	0x17: keyDeleteWord, // Ctrl-W
	0x15: keyClear,      // Ctrl-U
	0x10: keyUp,         // Ctrl-P
	0x0b: keyUp,         // Ctrl-K
	0x0e: keyDown,       // Ctrl-N
	'\n': keyDown,       // Ctrl-J
	'\t': keyExpand,
	0x12: keyToggleRegex,   // Ctrl-R
	0x16: keyTogglePreview, // Ctrl-V
	0x03: keyCancel,        // Ctrl-C
	0x07: keyCancel,        // Ctrl-G
}

// csiKeys maps the final byte of an ESC [ or ESC O sequence.
var csiKeys = map[byte]key{
	'A': keyUp,
	'B': keyDown,
	'C': keyExpand,
	'D': keyCollapse,
	'H': keyHome,
	'F': keyEnd,
	'Z': keyCollapse, // Shift-Tab
}

// tildeKeys maps the first parameter of an ESC [ n ~ sequence.
var tildeKeys = map[string]key{
	"1": keyHome, "7": keyHome,
	"4": keyEnd, "8": keyEnd,
	"5": keyPageUp,
	"6": keyPageDown,
}

// decode turns bytes read from the terminal into key events. A lone ESC at
// the end of a read is the Escape key; terminals send whole sequences in one
// write. Unknown keys are dropped.
func decode(b []byte) []event {
	var evs []event
	for len(b) > 0 {
		c := b[0]
		switch {
		case c == 0x1b:
			k, n := escape(b)
			if k >= 0 {
				evs = append(evs, event{key: k})
			}
			b = b[n:]
		case c < 0x20 || c == 0x7f:
			if k, ok := ctrlKeys[c]; ok {
				evs = append(evs, event{key: k})
			}
			b = b[1:]
		default:
			r, n := utf8.DecodeRune(b)
			if r != utf8.RuneError || n > 1 {
				evs = append(evs, event{key: keyRune, r: r})
			}
			b = b[n:]
		}
	}
	return evs
}

// escape decodes the sequence at the start of b, which begins with ESC. It
// returns the key, or -1 if unknown, and the number of bytes used.
func escape(b []byte) (key, int) {
	if len(b) < 2 || (b[1] != '[' && b[1] != 'O') {
		return keyCancel, 1
	}
	if b[1] == 'O' {
		if len(b) < 3 {
			return -1, len(b)
		}
		if k, ok := csiKeys[b[2]]; ok {
			return k, 3
		}
		return -1, 3
	}
	// CSI: parameter and intermediate bytes, then a final byte in 0x40–0x7e.
	for i := 2; i < len(b); i++ {
		if b[i] >= 0x40 && b[i] <= 0x7e {
			if b[i] == '~' {
				params := string(b[2:i])
				for j := 0; j < len(params); j++ {
					if params[j] == ';' {
						params = params[:j]
						break
					}
				}
				if k, ok := tildeKeys[params]; ok {
					return k, i + 1
				}
				return -1, i + 1
			}
			if k, ok := csiKeys[b[i]]; ok {
				return k, i + 1
			}
			return -1, i + 1
		}
	}
	return -1, len(b)
}
