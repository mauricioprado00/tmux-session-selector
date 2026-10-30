package ui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/mauricioprado00/tmux-session-selector/internal/filter"
	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

type kind int

const (
	sessionRow kind = iota
	windowRow
	paneRow
)

// node is one row of the tree. A window with a single pane has no pane row;
// the window row stands for that pane.
type node struct {
	kind     kind
	session  *tmux.Session
	window   *tmux.Window
	pane     *tmux.Pane // the pane row's pane, or the only pane of a window
	name     string     // session or window name, cleaned for display
	command  string
	path     string
	cmdWidth int // pane rows: the width the command column is padded to
	fields   []filter.Field
	parent   *node
	children []*node
	expanded bool
	current  bool
}

// buildTree turns sessions into rows. Sessions start expanded and windows
// collapsed; here marks the client's current location.
func buildTree(sessions []*tmux.Session, here tmux.Location, home string) (roots []*node, total int) {
	for _, s := range sessions {
		sn := &node{kind: sessionRow, session: s, name: clean(s.Name), expanded: true, current: s.ID == here.Session}
		sessionField := filter.Field{Kind: filter.Session, Value: sn.name}
		sn.fields = []filter.Field{sessionField}
		total++
		for _, w := range s.Windows {
			wn := &node{kind: windowRow, session: s, window: w, name: clean(w.Name), parent: sn,
				current: sn.current && w.ID == here.Window}
			windowField := filter.Field{Kind: filter.Window, Value: wn.name}
			wn.fields = []filter.Field{sessionField, windowField, {Kind: filter.Index, Value: strconv.Itoa(w.Index)}}
			total++
			if len(w.Panes) == 1 {
				wn.setPane(w.Panes[0], home)
			} else {
				width := 0
				for _, p := range w.Panes {
					pn := &node{kind: paneRow, session: s, window: w, parent: wn,
						current: wn.current && p.ID == here.Pane}
					pn.fields = []filter.Field{sessionField, windowField}
					pn.setPane(p, home)
					width = max(width, textWidth(pn.command))
					wn.children = append(wn.children, pn)
					total++
				}
				for _, pn := range wn.children {
					pn.cmdWidth = width
				}
			}
			sn.children = append(sn.children, wn)
		}
		roots = append(roots, sn)
	}
	return roots, total
}

func (n *node) setPane(p *tmux.Pane, home string) {
	n.pane = p
	n.command = clean(p.Command)
	n.path = clean(tilde(p.Path, home))
	n.fields = append(n.fields,
		filter.Field{Kind: filter.Command, Value: n.command},
		filter.Field{Kind: filter.Path, Value: n.path},
		filter.Field{Kind: filter.Title, Value: clean(p.Title)})
}

func tilde(path, home string) string {
	if home == "" || home == "/" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+"/") {
		return "~" + path[len(home):]
	}
	return path
}

// describe names the row for a question such as "Kill …?".
func (n *node) describe() string {
	switch n.kind {
	case sessionRow:
		return "session " + n.name
	case windowRow:
		return "window " + strconv.Itoa(n.window.Index) + ": " + n.name
	}
	return "pane " + n.pane.ID + " (" + n.command + ")"
}

// SortOrders are the orders the tree can be sorted in, as in tmux's chooser:
// sessions by id (creation) and windows by index; by name; or by time, most
// recently active first. Panes are always in index order.
var SortOrders = []string{"index", "name", "time"}

// sortTree sorts the tree in place by SortOrders[order], reversed if asked.
func sortTree(roots []*node, order int, reverse bool) {
	less := func(a, b *node) bool {
		switch {
		case a.kind == paneRow:
			return a.pane.Index < b.pane.Index
		case SortOrders[order] == "name":
			return a.name < b.name
		case SortOrders[order] == "time" && a.kind == sessionRow:
			return a.session.Activity > b.session.Activity
		case SortOrders[order] == "time":
			return a.window.Activity > b.window.Activity
		case a.kind == sessionRow:
			return idNumber(a.session.ID) < idNumber(b.session.ID)
		}
		return a.window.Index < b.window.Index
	}
	var sortNodes func(nodes []*node)
	sortNodes = func(nodes []*node) {
		sort.SliceStable(nodes, func(i, j int) bool {
			if reverse {
				return less(nodes[j], nodes[i])
			}
			return less(nodes[i], nodes[j])
		})
		for _, n := range nodes {
			sortNodes(n.children)
		}
	}
	sortNodes(roots)
}

// idNumber is the number in a tmux id such as $12.
func idNumber(id string) int {
	n, _ := strconv.Atoi(strings.TrimLeft(id, "$@%"))
	return n
}

// target is where Enter on this row goes.
func (n *node) target() tmux.Target {
	t := tmux.Target{Session: n.session.ID}
	if n.window != nil {
		t.Window = n.window.ID
	}
	if n.kind == paneRow {
		t.Pane = n.pane.ID
	}
	return t
}

// previewPane is the pane shown in the preview for this row.
func (n *node) previewPane() *tmux.Pane {
	switch {
	case n.pane != nil:
		return n.pane
	case n.window != nil:
		return n.window.ActivePane()
	}
	if w := n.session.ActiveWindow(); w != nil {
		return w.ActivePane()
	}
	return nil
}

// row is a visible row. context rows do not match the query themselves and
// are shown only because a descendant does.
type row struct {
	n       *node
	context bool
}

// visible returns the rows to show and how many of them match. With an empty
// query it follows the expanded state; otherwise it shows every match and its
// ancestors, ignoring the expanded state.
func visible(roots []*node, m *filter.Matcher) (rows []row, matches int) {
	if m.Empty() {
		var walk func(n *node)
		walk = func(n *node) {
			rows = append(rows, row{n: n})
			if n.expanded {
				for _, c := range n.children {
					walk(c)
				}
			}
		}
		for _, n := range roots {
			walk(n)
		}
		return rows, len(rows)
	}
	var walk func(n *node) bool
	walk = func(n *node) bool {
		self := m.Match(n.fields)
		if self {
			matches++
		}
		mark := len(rows)
		rows = append(rows, row{n: n, context: !self})
		shown := self
		for _, c := range n.children {
			if walk(c) {
				shown = true
			}
		}
		if !shown {
			rows = rows[:mark]
		}
		return shown
	}
	for _, n := range roots {
		walk(n)
	}
	return rows, matches
}
