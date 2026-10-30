package ui

import (
	"fmt"
	"strings"
	"unicode"
)

// SGR parameters used for styling.
const (
	styleDim      = "2"
	styleSelect   = "7"
	styleMatch    = "1;33"
	stylePath     = "36"
	styleError    = "31"
	styleCurrent  = "1"
	styleConfirm  = "1;31"
	styleBell     = "1;31"
	styleActivity = "1;33"
	styleTagged   = "1;35"
	styleNormal   = "1;7"
	styleHeading  = "1;36"
	styleKey      = "33"
	styleExample  = "32"
)

// span is a piece of a screen line. hl holds byte ranges of text to show as
// matches.
type span struct {
	text  string
	style string
	hl    [][2]int
}

// drawLine writes spans at screen row y (0-based), cut to width with "…".
// base styles the whole line; fill pads it to the full width in that style.
func drawLine(b *strings.Builder, y, width int, base string, fill bool, spans ...span) {
	fmt.Fprintf(b, "\x1b[%d;1H\x1b[0m", y+1)
	total := 0
	for _, s := range spans {
		total += textWidth(s.text)
	}
	limit := width
	if total > width {
		limit = width - 1
	}
	cur := ""
	setStyle := func(parts ...string) {
		style := joinStyles(parts...)
		if style != cur {
			b.WriteString("\x1b[0;" + style + "m")
			cur = style
		}
	}
	used := 0
spans:
	for _, s := range spans {
		for i, r := range s.text {
			w := runeWidth(r)
			if used+w > limit {
				break spans
			}
			match := ""
			if inRanges(i, s.hl) {
				match = styleMatch
			}
			setStyle(base, s.style, match)
			b.WriteRune(r)
			used += w
		}
	}
	if total > width && width > 0 {
		setStyle(base)
		b.WriteString("…")
		used++
	}
	if fill && used < width {
		setStyle(base)
		b.WriteString(strings.Repeat(" ", width-used))
		used = width
	}
	b.WriteString("\x1b[0m")
	// Erasing from the last column would erase the character written there.
	if used < width {
		b.WriteString("\x1b[K")
	}
}

func joinStyles(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ";")
}

func inRanges(i int, ranges [][2]int) bool {
	for _, r := range ranges {
		if i >= r[0] && i < r[1] {
			return true
		}
	}
	return false
}

// clean makes s safe to print: tabs become spaces and other control
// characters become '?'.
func clean(s string) string {
	if !strings.ContainsFunc(s, isControl) {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case isControl(r):
			return '?'
		}
		return r
	}, s)
}

func isControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r < 0xa0)
}

func textWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// runeWidth returns the number of terminal columns r takes.
func runeWidth(r rune) int {
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return 0
	}
	for _, w := range wide {
		if r < w[0] {
			break
		}
		if r <= w[1] {
			return 2
		}
	}
	return 1
}

// wide lists the main East Asian wide and emoji ranges, in order.
var wide = [][2]rune{
	{0x1100, 0x115f},
	{0x231a, 0x231b},
	{0x2329, 0x232a},
	{0x23e9, 0x23ec},
	{0x23f0, 0x23f0},
	{0x23f3, 0x23f3},
	{0x25fd, 0x25fe},
	{0x2614, 0x2615},
	{0x2648, 0x2653},
	{0x267f, 0x267f},
	{0x2693, 0x2693},
	{0x26a1, 0x26a1},
	{0x26aa, 0x26ab},
	{0x26bd, 0x26be},
	{0x26c4, 0x26c5},
	{0x26ce, 0x26ce},
	{0x26d4, 0x26d4},
	{0x26ea, 0x26ea},
	{0x26f2, 0x26f3},
	{0x26f5, 0x26f5},
	{0x26fa, 0x26fa},
	{0x26fd, 0x26fd},
	{0x2705, 0x2705},
	{0x270a, 0x270b},
	{0x2728, 0x2728},
	{0x274c, 0x274c},
	{0x274e, 0x274e},
	{0x2753, 0x2755},
	{0x2757, 0x2757},
	{0x2795, 0x2797},
	{0x27b0, 0x27b0},
	{0x27bf, 0x27bf},
	{0x2b1b, 0x2b1c},
	{0x2b50, 0x2b50},
	{0x2b55, 0x2b55},
	{0x2e80, 0x303e},
	{0x3041, 0x33ff},
	{0x3400, 0x4dbf},
	{0x4e00, 0x9fff},
	{0xa000, 0xa4cf},
	{0xa960, 0xa97f},
	{0xac00, 0xd7a3},
	{0xf900, 0xfaff},
	{0xfe10, 0xfe19},
	{0xfe30, 0xfe6f},
	{0xff00, 0xff60},
	{0xffe0, 0xffe6},
	{0x16fe0, 0x18cff},
	{0x1b000, 0x1b2ff},
	{0x1f004, 0x1f004},
	{0x1f0cf, 0x1f0cf},
	{0x1f18e, 0x1f18e},
	{0x1f191, 0x1f19a},
	{0x1f200, 0x1f2ff},
	{0x1f300, 0x1f64f},
	{0x1f680, 0x1f6ff},
	{0x1f7e0, 0x1f7eb},
	{0x1f90c, 0x1f9ff},
	{0x1fa70, 0x1faff},
	{0x20000, 0x3fffd},
}
