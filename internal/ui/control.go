package ui

import (
	"time"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

// frameGap is the least time between two frames drawn because panes
// printed: 30 a second at most, however much they print.
const frameGap = time.Second / 30

// controlState is the overview's control-mode client, which keeps it as up to date
// as tmux's own screen: a pane is captured again as soon as it prints, and
// the windows read again as soon as they change, rather than every refresh.
type controlState struct {
	ctl     *tmux.Control
	session string // the session it is attached to
	// What has happened since the last frame.
	panes   map[string]bool
	windows bool
	// failed are sessions a client could not stay attached to; their
	// overview falls back to capturing every refresh.
	failed map[string]bool
	// attached is when a client last attached to each session, and the
	// session's activity time before, as attaching sets it to now.
	attached map[string]attachTime
}

type attachTime struct{ at, before int64 }

// syncControl attaches a control-mode client to the overview's session
// while the overview is open and refreshing, and detaches it otherwise.
func (m *model) syncControl() {
	want := ""
	if m.overview != nil && m.refreshEvery > 0 {
		want = m.overview.session.ID
	}
	if want == m.live.session {
		return
	}
	m.stopControl()
	if m.live.failed == nil {
		m.live.failed = map[string]bool{}
	}
	if want == "" || m.live.failed[want] {
		return
	}
	ctl, err := m.tmux.Control(want)
	if err != nil {
		m.live.failed[want] = true
		return
	}
	if m.live.attached == nil {
		m.live.attached = map[string]attachTime{}
	}
	m.live.attached[want] = attachTime{at: time.Now().Unix(), before: m.overview.session.Activity}
	m.live.ctl, m.live.session = ctl, want
	// Screens captured before the client attached may be out of date.
	m.screens = map[string]screen{}
}

// stopControl detaches the control-mode client, if there is one. It is
// called before anything that acts on "the current client", which tmux
// could otherwise take to mean the control-mode client, as it is the one
// that attached last.
func (m *model) stopControl() {
	if m.live.ctl == nil {
		return
	}
	m.live.ctl.Close()
	m.live.ctl, m.live.session = nil, ""
}

// takeNews collects what the control-mode client has heard, and returns
// whether the overview has anything to draw again.
func (m *model) takeNews() bool {
	panes, windows, exited := m.live.ctl.Take()
	if exited {
		// The session closed, or the client could not attach: read the
		// windows again, which notices the first, and capture every
		// refresh from now on.
		m.live.failed[m.live.session] = true
		m.stopControl()
		m.live.windows = true
		return true
	}
	if m.live.panes == nil {
		m.live.panes = map[string]bool{}
	}
	shown := windows
	if m.overview != nil {
		for _, w := range m.overview.session.Windows {
			for _, p := range w.Panes {
				if panes[p.ID] {
					m.live.panes[p.ID], shown = true, true
				}
			}
		}
	}
	m.live.windows = m.live.windows || windows
	return shown
}

// applyNews forgets the screens of panes that printed, so the next frame
// captures them again, and reads the windows again if they changed.
func (m *model) applyNews() {
	if m.overview != nil {
		if m.live.windows {
			m.refreshOverview()
			// Panes may have changed size, or their program.
			m.screens = map[string]screen{}
			m.editorFiles = nil
		}
		for id := range m.live.panes {
			delete(m.screens, id)
		}
	}
	m.live.panes, m.live.windows = nil, false
}

// fixActivity puts back the activity time of sessions whose activity is
// only the control-mode client attaching, so that sorting by activity does
// not move a session for having been looked at in the overview.
func (m *model) fixActivity(sessions []*tmux.Session) {
	for _, s := range sessions {
		if a, ok := m.live.attached[s.ID]; ok && s.Activity >= a.at && s.Activity <= a.at+2 {
			s.Activity = a.before
		}
	}
}
