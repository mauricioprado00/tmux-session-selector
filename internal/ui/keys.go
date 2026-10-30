package ui

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

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
	keyHelp
	keyCancel
	keyMouse
	keyAlt
	keyTagAll
	keyToggleMode
	keyHalfDown
	keyMatrix
	keyTab
	keyBackTab
)

type event struct {
	key key
	r   rune // for keyRune and keyAlt
	// For keyMouse: the SGR button code (0 left, 64/65 wheel, plus modifier
	// bits), the 0-based cell, and whether it is a press or a release.
	button int
	x, y   int
	press  bool
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
	'\t': keyTab,
	0x12: keyToggleRegex,   // Ctrl-R
	0x16: keyTogglePreview, // Ctrl-V
	0x14: keyTagAll,        // Ctrl-T
	0x1f: keyToggleMode,    // Ctrl-/ (and Ctrl-_)
	0x02: keyPageUp,        // Ctrl-B
	0x06: keyPageDown,      // Ctrl-F
	0x04: keyHalfDown,      // Ctrl-D
	0x0f: keyMatrix,        // Ctrl-O
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
	'Z': keyBackTab, // Shift-Tab
	'P': keyHelp,    // F1
}

// tildeKeys maps the first parameter of an ESC [ n ~ sequence.
var tildeKeys = map[string]key{
	"1": keyHome, "7": keyHome,
	"4": keyEnd, "8": keyEnd,
	"5":  keyPageUp,
	"6":  keyPageDown,
	"11": keyHelp, // F1
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
			ev, n := escape(b)
			if ev.key >= 0 {
				evs = append(evs, ev)
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
// returns the event, with key -1 if unknown, and the number of bytes used.
func escape(b []byte) (event, int) {
	unknown := event{key: -1}
	switch {
	case len(b) < 2:
		return event{key: keyCancel}, 1
	case b[1] != '[' && b[1] != 'O':
		// ESC before a printable character is that character with Alt.
		if b[1] >= 0x20 && b[1] < 0x7f {
			return event{key: keyAlt, r: rune(b[1])}, 2
		}
		return event{key: keyCancel}, 1
	}
	if b[1] == 'O' {
		// SS3 for a known key; otherwise Alt-O.
		if len(b) >= 3 {
			if k, ok := csiKeys[b[2]]; ok {
				return event{key: k}, 3
			}
		}
		return event{key: keyAlt, r: 'O'}, 2
	}
	// CSI: parameter and intermediate bytes, then a final byte in 0x40–0x7e.
	for i := 2; i < len(b); i++ {
		if b[i] < 0x40 || b[i] > 0x7e {
			continue
		}
		params := string(b[2:i])
		switch {
		case strings.HasPrefix(params, "<") && (b[i] == 'M' || b[i] == 'm'):
			return mouse(params[1:], b[i] == 'M'), i + 1
		case b[i] == '~':
			first, _, _ := strings.Cut(params, ";")
			if k, ok := tildeKeys[first]; ok {
				return event{key: k}, i + 1
			}
		default:
			if k, ok := csiKeys[b[i]]; ok {
				return event{key: k}, i + 1
			}
		}
		return unknown, i + 1
	}
	return unknown, len(b)
}

// mouse decodes the parameters of an SGR mouse report, "button;x;y" with
// 1-based coordinates.
func mouse(params string, press bool) event {
	f := strings.Split(params, ";")
	if len(f) != 3 {
		return event{key: -1}
	}
	button, err1 := strconv.Atoi(f[0])
	x, err2 := strconv.Atoi(f[1])
	y, err3 := strconv.Atoi(f[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return event{key: -1}
	}
	return event{key: keyMouse, button: button, x: x - 1, y: y - 1, press: press}
}
