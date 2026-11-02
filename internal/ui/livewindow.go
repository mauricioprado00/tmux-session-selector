package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

// liveOption is the window option that marks a live window, one made from an
// overview by makeLiveWindow, and holds what splitLiveWindow needs to put
// its windows back.
const liveOption = "@tmux-session-selector-overview"

// savedWindow is a window joined into a live window, as it was.
type savedWindow struct {
	Name   string   `json:"name"`
	Index  int      `json:"index"`
	Layout string   `json:"layout"`
	Panes  []string `json:"panes"`
	Active string   `json:"active"`
	// AutoRename is the window's own automatic-rename option, or "" if it
	// used the global one; renaming a window turns it off.
	AutoRename string `json:"autoRename"`
	// Host is the window the others were joined into, which stays.
	Host bool `json:"host,omitempty"`
}

// split is an arrangement of windows as tmux layouts are made: nested rows
// and columns. A leaf holds a window.
type split struct {
	sideBySide bool
	weights    []int
	children   []*split
	window     *tmux.Window
}

// placed is a window and where the overview drew it.
type placed struct {
	box    cellBox
	window *tmux.Window
}

// guillotine turns the overview's boxes into nested splits, cutting into
// rows first, then columns, and so on, or returns nil if the boxes cannot
// be cut that way. Each part weighs as much as it spans, so empty space
// between boxes is shared out.
func guillotine(items []placed) *split {
	if len(items) == 1 {
		return &split{window: items[0].window}
	}
	for _, sideBySide := range []bool{false, true} {
		groups, extents := bands(items, sideBySide)
		if len(groups) < 2 {
			continue
		}
		s := &split{sideBySide: sideBySide, weights: extents}
		for _, g := range groups {
			c := guillotine(g)
			if c == nil {
				return nil
			}
			s.children = append(s.children, c)
		}
		return s
	}
	return nil
}

// bands groups items into runs that no straight cut separates: rows, or
// columns if sideBySide; extents are how far each spans.
func bands(items []placed, sideBySide bool) (groups [][]placed, extents []int) {
	span := func(b cellBox) (int, int) {
		if sideBySide {
			return b.x0, b.x1
		}
		return b.y0, b.y1
	}
	sorted := append([]placed(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, _ := span(sorted[i].box)
		b, _ := span(sorted[j].box)
		return a < b
	})
	var group []placed
	lo, hi := 0, -1
	for _, it := range sorted {
		start, end := span(it.box)
		if group != nil && start > hi {
			groups, extents = append(groups, group), append(extents, hi-lo+1)
			group = nil
		}
		if group == nil {
			lo, hi = start, end
		}
		hi = max(hi, end)
		group = append(group, it)
	}
	return append(groups, group), append(extents, hi-lo+1)
}

// place lays the splits out over w×h cells at (x, y) as a tmux layout, each
// window keeping its own panes' arrangement within its part.
func (s *split) place(x, y, w, h int) (*tmux.Layout, error) {
	if s.window != nil {
		l, err := tmux.ParseLayout(s.window.Layout)
		if err != nil {
			return nil, err
		}
		return l.Fit(x, y, w, h)
	}
	total := h
	if s.sideBySide {
		total = w
	}
	sizes, err := tmux.Share(total, s.weights)
	if err != nil {
		return nil, err
	}
	node := &tmux.Layout{W: w, H: h, X: x, Y: y, Pane: -1, SideBySide: s.sideBySide}
	at := 0
	for i, c := range s.children {
		var l *tmux.Layout
		if s.sideBySide {
			l, err = c.place(x+at, y, sizes[i], h)
		} else {
			l, err = c.place(x, y+at, w, sizes[i])
		}
		if err != nil {
			return nil, err
		}
		// A part split the same way as this one joins this one's parts.
		if len(l.Children) > 0 && l.SideBySide == node.SideBySide {
			node.Children = append(node.Children, l.Children...)
		} else {
			node.Children = append(node.Children, l)
		}
		at += sizes[i] + 1
	}
	return node, nil
}

// askLiveWindow asks before joining the overview's windows into one.
func (m *model) askLiveWindow() {
	ov := m.overview
	if len(ov.session.Windows) < 2 {
		m.status = "only one window: nothing to join"
		return
	}
	m.confirm = &confirmation{
		question: fmt.Sprintf("Join the %d windows of %s into one live window?", len(ov.session.Windows), clean(ov.session.Name)),
		action:   m.makeLiveWindow,
	}
}

// makeLiveWindow joins every window of the overview's session into the one
// in focus, as panes laid out the way the overview shows them, each window's
// own panes keeping their arrangement within its part. The window keeps what
// it was made from in liveOption, so the selector can split it back, and the
// client switches to it.
func (m *model) makeLiveWindow() (done bool, target *tmux.Target, err error) {
	m.refreshOverview()
	ov := m.overview
	if ov == nil || len(ov.session.Windows) < 2 {
		return false, nil, nil
	}
	s, tm := ov.session, m.tmux
	host := s.Windows[ov.focus]
	center := -1
	for i, w := range s.Windows {
		if w == ov.center {
			center = i
		}
	}
	var items []placed
	for i, box := range overviewLayout(s.Windows, center, max(ov.width, 1), max(ov.height, 1), m.sizes) {
		items = append(items, placed{box, s.Windows[i]})
	}

	// Save how the windows were, before anything changes.
	var saved []savedWindow
	for _, w := range s.Windows {
		sw := savedWindow{Name: w.Name, Index: w.Index, Layout: w.Layout, Host: w == host}
		for _, p := range w.Panes {
			sw.Panes = append(sw.Panes, p.ID)
		}
		if p := w.ActivePane(); p != nil {
			sw.Active = p.ID
		}
		out, _ := tm.Run("show-options", "-wqv", "-t", w.ID, "automatic-rename")
		sw.AutoRename = strings.TrimSpace(out)
		saved = append(saved, sw)
	}
	data, _ := json.Marshal(saved)
	if _, err := tm.Run("set-option", "-w", "-t", host.ID, liveOption, string(data)); err != nil {
		m.status = err.Error()
		return false, nil, nil
	}

	for _, w := range s.Windows {
		if w.Zoomed {
			tm.Run("resize-pane", "-Z", "-t", w.ID)
		}
	}
	for _, w := range s.Windows {
		if w == host {
			continue
		}
		for _, p := range w.Panes {
			// Tiling after each join keeps room for the next one.
			if _, err := tm.Run("join-pane", "-d", "-s", p.ID, "-t", host.ID); err != nil {
				m.status = err.Error()
				return m.reload(), nil, nil
			}
			tm.Run("select-layout", "-t", host.ID, "tiled")
		}
	}
	if tree := guillotine(items); tree == nil {
		m.status = "kept a tiled layout: the overview's arrangement has no tmux equivalent"
	} else if l, err := tree.place(0, 0, host.Width, host.Height); err != nil {
		m.status = "kept a tiled layout: " + err.Error()
	} else if err := tm.ApplyLayout(host.ID, l); err != nil {
		m.status = "kept a tiled layout: " + err.Error()
	}
	tm.Run("rename-window", "-t", host.ID, "overview")
	if p := host.ActivePane(); p != nil {
		tm.Run("select-pane", "-t", p.ID)
	}

	t := tmux.Target{Session: s.ID, Window: host.ID}
	if !m.inside {
		return true, &t, nil
	}
	if err := tm.Switch(m.client, t); err != nil {
		m.status = err.Error()
		return m.reload(), nil, nil
	}
	return true, nil, nil
}

// splitLiveWindow puts the windows a live window was made from back as they
// were: at their numbers if free, with their names, layouts and active
// panes. Panes closed since are skipped; panes added since stay in the live
// window. It reports whether window was a live window.
func splitLiveWindow(tm tmux.Tmux, window string) (bool, error) {
	out, err := tm.Run("show-options", "-wqv", "-t", window, liveOption)
	if err != nil || strings.TrimSpace(out) == "" {
		return false, nil
	}
	var saved []savedWindow
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &saved); err != nil {
		return true, fmt.Errorf("unreadable %s: %w", liveOption, err)
	}
	sessionID, err := tm.Run("display-message", "-p", "-t", window, "#{session_id}")
	if err != nil {
		return true, err
	}
	sessionID = strings.TrimSpace(sessionID)
	lines := func(args ...string) map[string]bool {
		out, _ := tm.Run(args...)
		set := map[string]bool{}
		for _, l := range strings.Fields(out) {
			set[l] = true
		}
		return set
	}
	alive := lines("list-panes", "-a", "-F", "#{pane_id}")
	taken := lines("list-windows", "-t", sessionID, "-F", "#{window_index}")

	var host savedWindow
	for _, w := range saved {
		if w.Host {
			host = w
			continue
		}
		var panes []string
		for _, p := range w.Panes {
			if alive[p] {
				panes = append(panes, p)
			}
		}
		if len(panes) == 0 {
			continue
		}
		args := []string{"break-pane", "-d", "-P", "-F", "#{window_id}", "-s", panes[0], "-n", w.Name}
		if index := fmt.Sprint(w.Index); !taken[index] {
			args = append(args, "-t", sessionID+":"+index)
			taken[index] = true
		}
		id, err := tm.Run(args...)
		if err != nil {
			return true, err
		}
		id = strings.TrimSpace(id)
		for _, p := range panes[1:] {
			tm.Run("join-pane", "-d", "-s", p, "-t", id)
			tm.Run("select-layout", "-t", id, "tiled")
		}
		restoreWindow(tm, id, w, alive)
	}
	restoreWindow(tm, window, host, alive)
	tm.Run("set-option", "-wu", "-t", window, liveOption)
	tm.Run("select-window", "-t", window)
	return true, nil
}

// restoreWindow gives window w's layout (if its panes are all there still),
// active pane, name and automatic renaming back.
func restoreWindow(tm tmux.Tmux, window string, w savedWindow, alive map[string]bool) {
	if l, err := tmux.ParseLayout(w.Layout); err == nil {
		tm.ApplyLayout(window, l)
	}
	if alive[w.Active] {
		tm.Run("select-pane", "-t", w.Active)
	}
	tm.Run("rename-window", "-t", window, w.Name)
	if w.AutoRename == "" {
		tm.Run("set-option", "-wu", "-t", window, "automatic-rename")
	} else {
		tm.Run("set-option", "-w", "-t", window, "automatic-rename", w.AutoRename)
	}
}
