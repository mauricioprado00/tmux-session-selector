package ui

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTypeMode(t *testing.T) {
	tm := tmuxServer(t)
	pane := tmuxRun(t, tm, "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", "a", "-n", "cat", "-x", "80", "-y", "24", "cat -v")
	tmuxRun(t, tm, "new-window", "-d", "-t", "a")
	settleNames(t, tm, "a")
	m := liveModel(t, tm)
	m.selectRow(t, "a")
	m.handle(event{key: keyOverview}) // focus on window 0, cat
	typeIn := func(s string) {
		t.Helper()
		if done, _, _ := m.terminalInput([]byte(s)); done {
			t.Fatal("typing should not end the selector")
		}
	}
	lines := func() []string {
		time.Sleep(200 * time.Millisecond)
		return strings.Split(tmuxRun(t, tm, "capture-pane", "-p", "-t", pane), "\n")
	}

	m.handle(typed('i'))
	if !m.isTyping() {
		t.Fatal("i should start type mode")
	}
	screen := m.render(160, 40)
	if !strings.Contains(screen, " TYPING ") || !strings.Contains(screen, "C-b Esc") || !strings.Contains(screen, "\x1b[?25h") {
		t.Errorf("type mode should show TYPING, how to stop, and the pane's cursor")
	}
	// The prefix twice sends it; the prefix and another key send both.
	typeIn("ab")
	typeIn("\x02")
	typeIn("\x02")
	typeIn("\x02x\r")
	if l := lines(); len(l) < 2 || l[0] != "ab^B^Bx" || l[1] != "ab^B^Bx" {
		t.Errorf("the pane got %q", l)
	}
	// The prefix then Esc stops typing; the keys after go to the overview.
	typeIn("\x02")
	typeIn("\x1b")
	if m.isTyping() || m.overview == nil {
		t.Fatalf("prefix Esc should stop typing and stay in the overview")
	}
	// Ctrl-] stops too, and what follows it is the overview's: Tab moves
	// the focus to the other window.
	m.handle(typed('i'))
	typeIn("z\x1d\t")
	if m.isTyping() || m.overview.focus != 1 {
		t.Errorf("Ctrl-] should stop typing, and Tab move the focus: typing %v, focus %d", m.isTyping(), m.overview.focus)
	}
	if l := lines(); len(l) < 3 || l[2] != "z" {
		t.Errorf("z before Ctrl-] should reach the pane: %q", l)
	}
	// The prefix then , renames the window, while typing and not; the
	// prompt starts from the name, and typing goes on after.
	m.overview.focus = 0
	m.handle(typed('i'))
	typeIn("\x02,")
	if m.input == nil || string(m.input.text) != "cat" {
		t.Fatalf("prefix , should ask for a new name, from the old one: %+v", m.input)
	}
	typeIn("\x15typed-in\r")
	if got := windowNames(t, tm, "a"); !strings.HasPrefix(got, "typed-in ") || !m.isTyping() {
		t.Errorf("after renaming while typing: %q, typing %v", got, m.isTyping())
	}
	typeIn("\x1d")
	typeIn("\x02,")
	typeIn("\x15plain\r")
	if got := windowNames(t, tm, "a"); !strings.HasPrefix(got, "plain ") || m.overview.session.Windows[0].Name != "plain" {
		t.Errorf("after renaming in the overview: %q", got)
	}
	// The prefix then a key it does not bind does nothing: Esc does not
	// leave the overview.
	typeIn("\x02\x1b")
	if m.overview == nil {
		t.Fatal("prefix Esc outside type mode should not leave the overview")
	}
	if !strings.Contains(m.render(200, 40), "C-b , rename") {
		t.Error("the footer should show prefix , rename")
	}

	// A click outside the pane stops typing and focuses what was clicked.
	m.overview.focus = 0
	m.handle(typed('i'))
	m.render(160, 40)
	box := m.overview.cells[1]
	typeIn("\x1b[<0;" + strconv.Itoa(box.x0+3) + ";" + strconv.Itoa(box.y0+3) + "M")
	if m.isTyping() || m.overview.focus != 1 {
		t.Errorf("a click on the other window: typing %v, focus %d", m.isTyping(), m.overview.focus)
	}
}

func TestParsePrefix(t *testing.T) {
	for v, want := range map[string]byte{"C-b": 2, "C-a": 1, "C-Space": 0, "`": '`', "C-\\": 0x1c} {
		if b, ok := parsePrefix(v); !ok || b != want {
			t.Errorf("parsePrefix(%q) = %d, %v", v, b, ok)
		}
	}
	for _, v := range []string{"None", "M-a", "F12"} {
		if _, ok := parsePrefix(v); ok {
			t.Errorf("parsePrefix(%q) should not be a key", v)
		}
	}
}
