package procinfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeTitle(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sessions", "42.json"),
		`{"pid":42,"sessionId":"abc","cwd":"/home/me/www/my.app","name":"my-app-3","nameSource":"derived"}`)
	transcript := filepath.Join(dir, "projects", "-home-me-www-my-app", "abc.jsonl")
	// A long transcript: the title changed, and lots came after it.
	var b strings.Builder
	b.WriteString(`{"type":"ai-title","aiTitle":"First idea","sessionId":"abc"}` + "\n")
	b.WriteString(strings.Repeat(`{"type":"user","message":"x"}`+"\n", 5000))
	b.WriteString(`{"type":"ai-title","aiTitle":"Fix the login form","sessionId":"abc"}` + "\n")
	b.WriteString(strings.Repeat(`{"type":"assistant","message":"`+strings.Repeat("y", 100)+`"}`+"\n", 1000))
	write(t, transcript, b.String())
	if got := ClaudeTitle(dir, 42); got != "Fix the login form" {
		t.Errorf("title = %q", got)
	}
	// A name the user gave wins.
	write(t, filepath.Join(dir, "sessions", "43.json"),
		`{"pid":43,"sessionId":"abc","cwd":"/home/me/www/my.app","name":"release prep","nameSource":"user"}`)
	if got := ClaudeTitle(dir, 43); got != "release prep" {
		t.Errorf("renamed title = %q", got)
	}
	// A custom title in the transcript wins over a later generated one.
	write(t, filepath.Join(dir, "sessions", "44.json"), `{"pid":44,"sessionId":"def","cwd":"/x"}`)
	write(t, filepath.Join(dir, "projects", "-x", "def.jsonl"),
		`{"type":"custom-title","customTitle":"Mine","sessionId":"def"}`+"\n"+
			`{"type":"ai-title","aiTitle":"Generated","sessionId":"def"}`+"\n")
	if got := ClaudeTitle(dir, 44); got != "Mine" {
		t.Errorf("custom title = %q", got)
	}
	if ClaudeTitle(dir, 99) != "" || ClaudeTitle("", 42) != "" {
		t.Error("no session file should give no title")
	}
}

func TestFiles(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"vim", "notes.md"}, "notes.md"},
		{[]string{"/usr/bin/nvim", "-O", "a.go", "b.go"}, "a.go b.go"},
		{[]string{"vim", "-c", "set nu", "+12", "main.go"}, "main.go"},
		{[]string{"vim", "--", "-weird"}, "-weird"},
		{[]string{"nano", "-T", "4", "/etc/hosts"}, "/etc/hosts"},
		{[]string{"vim"}, ""},
		{[]string{"less", "file"}, ""},
	}
	for _, tt := range tests {
		if got := Files(tt.args); got != tt.want {
			t.Errorf("Files(%q) = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func TestEditorFiles(t *testing.T) {
	proc := t.TempDir()
	// The shell 100's terminal has 300 in the foreground: vim on two files.
	write(t, filepath.Join(proc, "100", "stat"), "100 (bash) S 1 100 100 34817 300 4194304 0 0")
	write(t, filepath.Join(proc, "300", "cmdline"), "vim\x00-p\x00a.txt\x00b (1).txt\x00")
	if got := EditorFiles(proc, 100); got != "a.txt b (1).txt" {
		t.Errorf("EditorFiles = %q", got)
	}
	// A command name with spaces and parentheses.
	write(t, filepath.Join(proc, "101", "stat"), "101 (my (odd) sh) S 1 101 101 34818 301 0")
	write(t, filepath.Join(proc, "301", "cmdline"), "nano\x00todo\x00")
	if got := EditorFiles(proc, 101); got != "todo" {
		t.Errorf("EditorFiles with odd comm = %q", got)
	}
	if EditorFiles(proc, 999) != "" || EditorFiles("", 100) != "" {
		t.Error("missing process or no /proc should give nothing")
	}
}
