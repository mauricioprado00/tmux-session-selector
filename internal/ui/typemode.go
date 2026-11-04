package ui

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

// Type mode: i in the overview sends the keys typed to the active pane of
// the window in focus, as if typing in it, while the overview keeps
// showing every window. The pane is framed in red, the top line says
// TYPING, the pane's cursor is shown, and the bottom line says how to stop:
// tmux's prefix then Esc, as tmux would have it, or Ctrl-], which also
// works where tmux keeps the prefix for itself (the selector in a pane
// rather than a popup). The prefix twice sends the prefix. A click on
// another window stops typing and focuses it.

const (
	styleTyping      = "1;31"
	styleTypingBadge = "1;7;31"
	keyStopTyping    = 0x1d // Ctrl-]
)

// typing is the overview's type mode.
type typing struct {
	pane, window string // ids
	// Where the pane is on screen, at the last frame, for the mouse.
	box cellBox
}

// prefixKey is tmux's prefix key, as a byte a terminal sends, and its name,
// read once; ok is false when the prefix is not such a key (None, or a key
// with Alt), and only Ctrl-] stops typing.
type prefixKey struct {
	b        byte
	name     string
	ok, read bool
}

// parsePrefix turns tmux's prefix option, such as C-b or `, into the byte a
// terminal sends for it.
func parsePrefix(v string) (byte, bool) {
	switch {
	case v == "C-Space" || v == "C-@":
		return 0, true
	case len(v) == 3 && strings.HasPrefix(v, "C-") && v[2] >= 'a' && v[2] <= 'z':
		return v[2] - 'a' + 1, true
	case len(v) == 3 && strings.HasPrefix(v, "C-") && v[2] >= '[' && v[2] <= '_':
		return v[2] - '@', true
	case len(v) == 1 && v[0] > 0x20 && v[0] < 0x7f:
		return v[0], true
	}
	return 0, false
}

// setPrefix takes tmux's prefix option, as C-b.
func (m *model) setPrefix(name string) {
	m.prefix.read, m.prefix.name = true, name
	m.prefix.b, m.prefix.ok = parsePrefix(name)
	m.prefix.ok = m.prefix.ok && m.prefix.b != keyStopTyping
}

// readPrefix reads tmux's prefix, if the pane list has not brought it, and
// returns whether it is a key the selector can see.
func (m *model) readPrefix() bool {
	if !m.prefix.read {
		m.prefix.read = true
		if out, err := m.tmux.Run("show-options", "-gv", "prefix"); err == nil {
			m.setPrefix(strings.TrimSpace(out))
		}
	}
	return m.prefix.ok
}

// startTyping starts type mode on the active pane of the window in focus.
func (m *model) startTyping() {
	ov := m.overview
	w := ov.session.Windows[ov.focus]
	p := w.ActivePane()
	if p == nil {
		return
	}
	m.readPrefix()
	ov.typing, ov.prefixed = &typing{pane: p.ID, window: w.ID}, false
}

// isTyping is whether the overview is in type mode.
func (m *model) isTyping() bool { return m.overview != nil && m.overview.typing != nil }

// typeInput sends bytes read from the terminal to the pane being typed
// into, until a key that stops typing; it returns the bytes after that key,
// for the overview, or nil.
func (m *model) typeInput(b []byte) (rest []byte, done bool, target *tmux.Target, err error) {
	ov := m.overview
	ty := ov.typing
	var send []byte
	flush := func() {
		if keys := tmux.Input(send); len(keys) > 0 {
			if err := m.tmux.SendKeys(ty.pane, keys); err != nil {
				m.status = "could not type into " + ty.pane + ": " + err.Error()
			}
		}
		send = nil
	}
	stop := func() { flush(); m.overview.typing = nil }
	for len(b) > 0 {
		// One key's bytes: a whole escape sequence, a character.
		n := 1
		var ev event
		switch c := b[0]; {
		case c == 0x1b:
			ev, n = escape(b)
		case c >= 0x80:
			if _, size := utf8.DecodeRune(b); size > 1 {
				n = size
			}
		}
		k := b[:n]
		switch {
		case ov.prefixed && len(b) == 1 && b[0] == 0x1b: // prefix, Esc
			ov.prefixed = false
			stop()
			return nil, false, nil, nil
		case ov.prefixed && b[0] == ',' && n == 1: // prefix , renames the window, as in tmux
			ov.prefixed = false
			flush()
			m.renameWindowPrompt(ty.window)
			return b[1:], false, nil, nil
		case ov.prefixed && m.prefix.ok && b[0] == m.prefix.b && n == 1: // the prefix twice
			ov.prefixed = false
			send = append(send, k...)
		case ov.prefixed:
			// The prefix and another key: both go to the pane.
			ov.prefixed = false
			send = append(send, m.prefix.b)
			continue // the key again, as any other
		case m.prefix.ok && b[0] == m.prefix.b && n == 1:
			ov.prefixed = true
		case b[0] == keyStopTyping && n == 1:
			stop()
			return b[1:], false, nil, nil
		case ev.key == keyMouse:
			// A click outside the pane stops typing and acts as in the
			// overview; the rest of the mouse is not for the pane.
			if ev.press && ev.button&^(4|8|16) == 0 && !ty.box.contains(ev.x, ev.y) {
				stop()
				done, target, err = m.overviewMouse(ev)
				return b[n:], done, target, err
			}
		default:
			send = append(send, k...)
		}
		b = b[n:]
	}
	flush()
	return nil, false, nil, nil
}

// typingHeader is the top line's left part in type mode.
func (m *model) typingHeader() []span {
	ov := m.overview
	what := ov.typing.pane
	for _, w := range ov.session.Windows {
		for _, p := range w.Panes {
			if p.ID == ov.typing.pane {
				what = p.ID + " " + clean(p.Command) + " in window " + strconv.Itoa(w.Index) + ": " + clean(w.Name)
			}
		}
	}
	return []span{{text: " TYPING ", style: styleTypingBadge}, {text: " " + clean(ov.session.Name), style: styleHeading},
		{text: "  into " + what, style: styleDim}}
}

// typingFooter is the bottom line in type mode: how to stop, in red.
func (m *model) typingFooter() []span {
	var s []span
	if m.prefix.ok {
		s = append(s, span{text: " " + m.prefix.name + " Esc", style: styleTyping}, span{text: " or", style: styleDim})
	}
	s = append(s, span{text: " C-]", style: styleTyping}, span{text: " stop typing", style: styleDim})
	if m.prefix.ok {
		s = append(s, span{text: "    " + m.prefix.name + " " + m.prefix.name, style: styleTyping},
			span{text: " send " + m.prefix.name, style: styleDim},
			span{text: "    " + m.prefix.name + " ,", style: styleTyping}, span{text: " rename the window", style: styleDim})
	}
	return append(s, span{text: "    click another window", style: styleTyping}, span{text: " stop and focus it", style: styleDim})
}

// drawTyping frames the pane being typed into, records where it is, and
// returns the screen cell (1-based) of its cursor, or 0, 0 if not shown.
func (m *model) drawTyping(c *canvas, w *tmux.Window, box cellBox) (cx, cy int) {
	ty := m.overview.typing
	for _, r := range paneRects(w, box.x0, box.y0, box.x1, box.y1) {
		if r.p.ID != ty.pane {
			continue
		}
		c.restyleBox(r.x0, r.y0, r.x1, r.y1, styleTyping)
		c.text(r.x0+2, r.y1, r.x1-1, " typing ", styleTypingBadge)
		ty.box = cellBox{r.x0, r.y0 + 1, r.x1, r.y1 + 1} // screen rows are one below the canvas's
		iw, ih := r.x1-r.x0-1, r.y1-r.y0-1
		if iw < 1 || ih < 1 {
			return 0, 0
		}
		px, py := m.paneCursor(r.p)
		ox, oy := blitOffset(iw, ih, px, py)
		return r.x0 + 1 + px - ox + 1, r.y0 + 1 + py - oy + 2
	}
	return 0, 0
}

// hasPane is whether pane id is in sessions.
func hasPane(sessions []*tmux.Session, id string) bool {
	for _, s := range sessions {
		for _, w := range s.Windows {
			for _, p := range w.Panes {
				if p.ID == id {
					return true
				}
			}
		}
	}
	return false
}

// overviewPrefix handles tmux's prefix in the overview outside type mode,
// where tmux does not see it (in the popup): the prefix then , renames the
// window in focus, as in tmux, and the prefix then any other key does
// nothing, as an unbound key in tmux. It returns the bytes left for the
// overview's keys, and whether it took any; bytes before a prefix are
// returned on their own, with the prefix left for the next call.
func (m *model) overviewPrefix(raw []byte) (rest []byte, took bool) {
	ov := m.overview
	if ov.prefixed {
		ov.prefixed = false
		if raw[0] == ',' {
			w := ov.session.Windows[ov.focus]
			m.renameWindowPrompt(w.ID)
		}
		_, n := escape(raw)
		if raw[0] != 0x1b {
			n = 1
			if _, size := utf8.DecodeRune(raw); size > 1 {
				n = size
			}
		}
		return raw[n:], true
	}
	switch i := bytes.IndexByte(raw, m.prefix.b); {
	case i == 0:
		ov.prefixed = true
		return raw[1:], true
	case i > 0:
		return raw[:i], false // the rest comes back next
	}
	return raw, false
}

// renameWindowPrompt asks on the top line for a new name for window id,
// starting from its name, as tmux's prefix , does.
func (m *model) renameWindowPrompt(id string) {
	for _, w := range m.overview.session.Windows {
		if w.ID != id {
			continue
		}
		m.input = &inputPrompt{
			label: "rename window " + strconv.Itoa(w.Index) + ": ",
			text:  []rune(w.Name),
			action: func(name string) (bool, *tmux.Target, error) {
				if name == "" {
					return false, nil, nil
				}
				if _, err := m.tmux.Run("rename-window", "-t", id, "--", name); err != nil {
					m.status = err.Error()
				}
				return m.afterOverviewAction(false, nil, nil)
			},
		}
	}
}
