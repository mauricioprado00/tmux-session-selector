package ui

import (
	"strconv"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

// The overview's commands, as normal mode's in the list, on the window in
// focus: t tags it, T untags everything, x kills it and X kills the tagged
// items (both asking first), and : runs a command on the tagged items or
// else the window. Tags are the list's: what is tagged in one is tagged in
// the other.

// focusTarget is the window in focus, as a target, and its description.
func (m *model) focusTarget() (tmux.Target, string) {
	ov := m.overview
	w := ov.session.Windows[ov.focus]
	return tmux.Target{Session: ov.session.ID, Window: w.ID}, "window " + strconv.Itoa(w.Index) + ": " + w.Name
}

// windowTagged is whether window w of the overview's session is tagged.
func (m *model) windowTagged(w *tmux.Window) bool {
	_, ok := m.tags[tmux.Target{Session: m.overview.session.ID, Window: w.ID}.String()]
	return ok
}

// overviewCommand runs command key r of the overview: t, T, x, X or :.
func (m *model) overviewCommand(r rune) {
	ov := m.overview
	t, what := m.focusTarget()
	switch r {
	case 't':
		if _, on := m.tags[t.String()]; on {
			delete(m.tags, t.String())
		} else {
			if m.tags == nil {
				m.tags = map[string]tmux.Target{}
			}
			m.tags[t.String()] = t
		}
		ov.focus = (ov.focus + 1) % len(ov.session.Windows) // as t moves down the list
	case 'T':
		m.tags = nil
	case 'x':
		m.confirm = &confirmation{
			question: "Kill " + what + "?",
			action: func() (bool, *tmux.Target, error) {
				delete(m.tags, t.String())
				return m.afterOverviewAction(m.kill([]tmux.Target{t}))
			},
		}
	case 'X':
		targets, what := m.overviewTargets()
		m.confirm = &confirmation{
			question: "Kill " + what + "?",
			action: func() (bool, *tmux.Target, error) {
				m.tags = nil
				return m.afterOverviewAction(m.kill(targets))
			},
		}
	case ':', ';':
		targets, what := m.overviewTargets()
		m.input = &inputPrompt{
			label: "command for " + what + ": ",
			action: func(text string) (bool, *tmux.Target, error) {
				return m.afterOverviewAction(m.runEach(text, targets))
			},
		}
	}
}

// overviewTargets is what X and : act on in the overview: the tagged
// items, as in the list, or else the window in focus.
func (m *model) overviewTargets() ([]tmux.Target, string) {
	if len(m.tags) > 0 {
		return m.actionTargets()
	}
	t, what := m.focusTarget()
	return []tmux.Target{t}, what
}

// afterOverviewAction reads the overview's session again after a kill or a
// command, which has read the list again, so closed windows go; leaving the
// overview then reads the list once more, without the control-mode client.
func (m *model) afterOverviewAction(done bool, target *tmux.Target, err error) (bool, *tmux.Target, error) {
	if m.overview != nil && !done {
		m.overview.changed = true
		m.refreshOverview()
	}
	return done, target, err
}

// inputKey edits the line being typed for a command, in the list or the
// overview: Enter runs it, Esc drops it.
func (m *model) inputKey(ev event) (done bool, target *tmux.Target, err error) {
	p := m.input
	switch ev.key {
	case keyRune:
		p.text = append(p.text, ev.r)
	case keyBackspace:
		if len(p.text) > 0 {
			p.text = p.text[:len(p.text)-1]
		}
	case keyDeleteWord:
		p.text = deleteWord(p.text)
	case keyClear:
		p.text = nil
	case keyCancel:
		m.input = nil
	case keyEnter:
		m.input = nil
		return p.action(string(p.text))
	}
	return false, nil, nil
}
