package tmuxconf

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

var selectBegin, _ = markers(Select)

func TestDefaultPath(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	if got := DefaultPath(home, xdg); got != filepath.Join(home, ".tmux.conf") {
		t.Errorf("with no file, got %s", got)
	}
	os.MkdirAll(filepath.Join(home, ".config", "tmux"), 0o755)
	os.WriteFile(filepath.Join(home, ".config", "tmux", "tmux.conf"), nil, 0o644)
	if got := DefaultPath(home, xdg); got != filepath.Join(home, ".config", "tmux", "tmux.conf") {
		t.Errorf("with ~/.config/tmux/tmux.conf, got %s", got)
	}
	os.WriteFile(filepath.Join(home, ".tmux.conf"), nil, 0o644)
	if got := DefaultPath(home, xdg); got != filepath.Join(home, ".tmux.conf") {
		t.Errorf("~/.tmux.conf comes first, got %s", got)
	}
}

func TestAddRemove(t *testing.T) {
	b := Binding{Key: "w", Argv: []string{"/opt/my tools/sel", "-normal"}, Open: Popup,
		Previous: "bind-key -T prefix w choose-tree -Zw"}
	for _, original := range []string{"", "set -g mouse on\n", "set -g mouse on", "a\n\n"} {
		added := Add(original, b)
		if !strings.Contains(added, b.Line()) || strings.Count(added, selectBegin) != 1 {
			t.Fatalf("Add(%q) = %q", original, added)
		}
		again := Add(added, Binding{Key: "W", Argv: []string{"/sel"}, Open: Popup})
		if strings.Count(again, selectBegin) != 1 || !strings.Contains(again, "# key: W") {
			t.Errorf("adding twice should replace the block: %q", again)
		}
		rest, got := Remove(added, Select)
		if got == nil || got.Key != "w" || got.Previous != b.Previous {
			t.Errorf("Remove found %+v", got)
		}
		want := original
		if want != "" && !strings.HasSuffix(want, "\n") {
			want += "\n"
		}
		if rest != want {
			t.Errorf("Remove(Add(%q)) = %q", original, rest)
		}
	}
	if rest, got := Remove("set -g mouse on\n", Select); got != nil || rest != "set -g mouse on\n" {
		t.Errorf("Remove without a block = %q, %+v", rest, got)
	}
}

func TestLine(t *testing.T) {
	b := Binding{Key: "C-s", Argv: []string{"/home/me/my bin/sel", "-sort", "time", "it's $HOME"}, Open: Popup}
	want := `bind-key -T prefix C-s display-popup -E -w 80% -h 80% "'/home/me/my bin/sel' -sort time 'it'\\''s \$HOME'"`
	if got := b.Line(); got != want {
		t.Errorf("Line =\n%s\nwant\n%s", got, want)
	}
	b.Open = Window
	if got := b.Line(); !strings.Contains(got, "new-window -n select ") {
		t.Errorf("window: %s", got)
	}
	b.Open = Full
	if got := b.Line(); !strings.Contains(got, "display-popup -E -B -w 100% -h 100% ") {
		t.Errorf("full: %s", got)
	}
	b.Border = true
	if got := b.Line(); !strings.Contains(got, "display-popup -E -w 100% -h 100% ") {
		t.Errorf("full with a border: %s", got)
	}
}

func TestSupported(t *testing.T) {
	tests := []struct {
		open, version, want string
		note                bool
	}{
		{Popup, "tmux 3.4", Popup, false},
		{Full, "tmux 3.4", Full, false},
		{Full, "tmux 3.3a", Full, false},
		{Full, "tmux 3.2a", Full, true}, // with a border
		{Full, "tmux 3.1c", Window, true},
		{Popup, "tmux 2.9", Window, true},
		{Window, "tmux 2.9", Window, false},
		{Full, "tmux next-3.5", Full, false},
		{Full, "tmux master", Full, false},
	}
	for _, tt := range tests {
		got, note := Supported(tt.open, tt.version)
		if got != tt.want || (note != "") != tt.note {
			t.Errorf("Supported(%s, %q) = %s, %q", tt.open, tt.version, got, note)
		}
	}
}

func TestInstallUninstall(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	tm := tmux.Tmux{Args: []string{"-S", filepath.Join(dir, "sock"), "-f", "/dev/null"}}
	if _, err := tm.Run("new-session", "-d"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tm.Run("kill-server") })
	binding := func(key string) string {
		out, _ := tm.Run("list-keys", "-T", "prefix", key)
		return strings.TrimSpace(out)
	}
	config := filepath.Join(dir, "conf", "tmux.conf")
	os.MkdirAll(filepath.Dir(config), 0o755)
	original := "set -g mouse on\n"
	os.WriteFile(config, []byte(original), 0o600)

	b := Binding{Key: "w", Argv: []string{"/opt/sel tool/sel"}, Open: Popup}
	if live, err := Install(tm, config, b); err != nil || !live {
		t.Fatalf("Install = %v, %v", live, err)
	}
	if got := binding("w"); !strings.Contains(got, "display-popup") || !strings.Contains(got, "/opt/sel tool/sel") {
		t.Errorf("server binding of w = %q", got)
	}
	data, _ := os.ReadFile(config)
	if !strings.Contains(string(data), "# previous: bind-key -T prefix w choose-tree -Zw") {
		t.Errorf("config should remember tmux's binding of w:\n%s", data)
	}
	if info, _ := os.Stat(config); info.Mode().Perm() != 0o600 {
		t.Errorf("mode changed to %v", info.Mode().Perm())
	}
	// The file must load in a fresh server.
	if _, err := tm.Run("source-file", config); err != nil {
		t.Errorf("source-file: %v", err)
	}

	// Moving to another key restores w and keeps one block.
	if _, err := Install(tm, config, Binding{Key: "W", Argv: []string{"/sel"}, Open: Popup}); err != nil {
		t.Fatal(err)
	}
	if got := binding("w"); got != "bind-key -T prefix w choose-tree -Zw" {
		t.Errorf("w should be restored, got %q", got)
	}
	removed, live, err := Uninstall(tm, config, Select)
	if err != nil || !live || removed == nil || removed.Key != "W" {
		t.Fatalf("Uninstall = %+v, %v, %v", removed, live, err)
	}
	if got := binding("W"); got != "" {
		t.Errorf("W should be unbound, got %q", got)
	}
	if data, _ := os.ReadFile(config); string(data) != original {
		t.Errorf("config after Uninstall = %q, want the original", data)
	}
	if removed, _, _ := Uninstall(tm, config, Select); removed != nil {
		t.Error("a second Uninstall should find nothing")
	}
}

func TestTwoBindings(t *testing.T) {
	sel := Binding{Action: Select, Key: "w", Argv: []string{"/sel"}, Open: Full}
	ov := Binding{Action: Overview, Key: "O", Argv: []string{"/sel", "-overview"}, Open: Full}
	both := Add(Add("set -g mouse on\n", sel), ov)
	if !strings.Contains(both, sel.Line()) || !strings.Contains(both, ov.Line()) {
		t.Fatalf("both bindings should be in the file:\n%s", both)
	}
	// Replacing one leaves the other.
	both = Add(both, Binding{Action: Overview, Key: "W", Argv: []string{"/sel", "-overview"}, Open: Full})
	if strings.Count(both, "-overview") != 1 || !strings.Contains(both, "# key: W") || !strings.Contains(both, sel.Line()) {
		t.Errorf("replacing the overview binding:\n%s", both)
	}
	rest, removed := Remove(both, Overview)
	if removed == nil || removed.Key != "W" || removed.Action != Overview || strings.Contains(rest, "overview") || !strings.Contains(rest, sel.Line()) {
		t.Errorf("removing the overview binding left:\n%s", rest)
	}
	if rest, _ = Remove(rest, Select); rest != "set -g mouse on\n" {
		t.Errorf("removing both should restore the file, got %q", rest)
	}
}
