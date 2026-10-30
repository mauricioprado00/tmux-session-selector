package ui

import (
	"fmt"
	"strings"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

// menu is the right-click menu of a row, drawn over the screen at (x, y).
type menu struct {
	x, y  int
	items []menuItem
	sel   int
}

// menuItem is one entry; an empty label is a separator. key runs it while
// the menu is open (tmux's letter for it), hint shows how to do the same
// without the menu.
type menuItem struct {
	label  string
	key    rune
	hint   string
	action func() (done bool, target *tmux.Target, err error)
}

// openMenu opens the menu for the selected row at screen cell (x, y), with
// the same items as tmux's chooser.
func (m *model) openMenu(x, y int) {
	n := m.selected()
	if n == nil {
		return
	}
	command := func(r rune) func() (bool, *tmux.Target, error) {
		return func() (bool, *tmux.Target, error) { return m.command(r) }
	}
	expand := menuItem{label: "Expand", key: 'l', hint: "→", action: func() (bool, *tmux.Target, error) {
		return m.handle(event{key: keyExpand})
	}}
	if n.expanded && len(n.children) > 0 {
		expand = menuItem{label: "Collapse", key: 'h', hint: "←", action: func() (bool, *tmux.Target, error) {
			return m.handle(event{key: keyCollapse})
		}}
	}
	tag := menuItem{label: "Tag", key: 't', hint: "M-t", action: command('t')}
	if m.tagged(n) {
		tag.label = "Untag"
	}
	m.menu = &menu{x: x, y: y, items: []menuItem{
		{label: "Select", key: '\r', hint: "Enter", action: func() (bool, *tmux.Target, error) {
			return m.jump(n.target())
		}},
		expand,
		{label: "Mark", key: 'm', hint: "M-m", action: command('m')},
		{},
		tag,
		{label: "Tag All", key: 'a', hint: "C-t", action: func() (bool, *tmux.Target, error) {
			return m.handle(event{key: keyTagAll})
		}},
		{label: "Tag None", key: 'T', hint: "M-T", action: command('T')},
		{},
		{label: "Kill", key: 'x', hint: "M-x", action: command('x')},
		{label: "Kill Tagged", key: 'X', hint: "M-X", action: command('X')},
		{},
		{label: "Cancel", key: 'q', hint: "Esc", action: func() (bool, *tmux.Target, error) {
			return false, nil, nil
		}},
	}}
}

// menuKey handles an event while the menu is open. Any event that does not
// pick an item or move within the menu closes it.
func (m *model) menuKey(ev event) (done bool, target *tmux.Target, err error) {
	mn := m.menu
	run := func(i int) (bool, *tmux.Target, error) {
		m.menu = nil
		return mn.items[i].action()
	}
	switch ev.key {
	case keyMouse:
		if !ev.press {
			return false, nil, nil
		}
		w, _ := mn.size()
		if i := ev.y - mn.y - 1; ev.button == 0 && ev.x > mn.x && ev.x < mn.x+w-1 &&
			i >= 0 && i < len(mn.items) && mn.items[i].label != "" {
			return run(i)
		}
	case keyUp, keyDown:
		d := 1
		if ev.key == keyUp {
			d = -1
		}
		for i := mn.sel + d; i >= 0 && i < len(mn.items); i += d {
			if mn.items[i].label != "" {
				mn.sel = i
				break
			}
		}
		return false, nil, nil
	case keyEnter:
		return run(mn.sel)
	case keyRune:
		for i, it := range mn.items {
			if it.label != "" && it.key == ev.r {
				return run(i)
			}
		}
	}
	m.menu = nil
	return false, nil, nil
}

// size is the menu's width and height, borders included.
func (mn *menu) size() (int, int) {
	w := 0
	for _, it := range mn.items {
		w = max(w, textWidth(it.label)+2+textWidth(it.hint))
	}
	return w + 4, len(mn.items) + 2
}

// draw writes the menu over the screen, moved left or up to fit.
func (mn *menu) draw(b *strings.Builder, width, height int) {
	w, h := mn.size()
	mn.x = max(0, min(mn.x, width-w))
	mn.y = max(0, min(mn.y, height-h))
	line := func(y int, s, style string) {
		fmt.Fprintf(b, "\x1b[%d;%dH\x1b[0;%sm%s\x1b[0m", mn.y+y+1, mn.x+1, style, s)
	}
	line(0, "┌"+strings.Repeat("─", w-2)+"┐", "")
	for i, it := range mn.items {
		if it.label == "" {
			line(i+1, "├"+strings.Repeat("─", w-2)+"┤", "")
			continue
		}
		pad := strings.Repeat(" ", w-4-textWidth(it.label)-textWidth(it.hint))
		style := ""
		if i == mn.sel {
			style = styleSelect
		}
		line(i+1, "│", "")
		fmt.Fprintf(b, "\x1b[0;%sm %s%s\x1b[0;%sm%s \x1b[0m│", style, it.label, pad, joinStyles(style, styleDim), it.hint)
	}
	line(h-1, "└"+strings.Repeat("─", w-2)+"┘", "")
}
