// Package procinfo reads what a pane's processes are working on, cheaply
// enough to do while drawing: the session title of a Claude Code process,
// and the files an editor has open.
package procinfo

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ClaudeTitle is the title of the Claude Code session that process pid
// runs, or "" if it is not one or has none yet. Claude Code keeps a file per
// process, ~/.claude/sessions/PID.json, naming the session and its
// directory; the session's transcript, under ~/.claude/projects, records
// its title as it changes, so the latest one is near the end. A name the
// user gave (/rename) wins over the generated title.
func ClaudeTitle(claudeDir string, pid int) string {
	if claudeDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(claudeDir, "sessions", strconv.Itoa(pid)+".json"))
	if err != nil {
		return ""
	}
	var s struct {
		SessionID  string `json:"sessionId"`
		Cwd        string `json:"cwd"`
		Name       string `json:"name"`
		NameSource string `json:"nameSource"`
	}
	if json.Unmarshal(data, &s) != nil || s.SessionID == "" || strings.ContainsAny(s.SessionID, `/\`) {
		return ""
	}
	if s.Name != "" && s.NameSource != "" && s.NameSource != "derived" {
		return s.Name
	}
	// The project directory is the session's directory with every
	// character other than a letter or digit made a dash.
	project := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, s.Cwd)
	return lastTitle(filepath.Join(claudeDir, "projects", project, s.SessionID+".jsonl"))
}

// lastTitle finds the latest title in a transcript, reading it backwards
// from the end in growing pieces, so a long transcript costs no more than
// its last few title entries.
func lastTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return ""
	}
	for size := int64(64 << 10); ; size *= 4 {
		start := max(0, end-size)
		buf := make([]byte, end-start)
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return ""
		}
		if t := titleIn(buf); t != "" || start == 0 || size >= 16<<20 {
			return t
		}
	}
}

// titleIn is the title of the last custom-title line in buf, or else of
// the last ai-title line.
func titleIn(buf []byte) string {
	for _, kind := range []struct{ typ, field string }{
		{`"type":"custom-title"`, "customTitle"},
		{`"type":"ai-title"`, "aiTitle"},
	} {
		i := bytes.LastIndex(buf, []byte(kind.typ))
		if i < 0 {
			continue
		}
		lineStart := bytes.LastIndexByte(buf[:i], '\n') + 1
		lineEnd := bytes.IndexByte(buf[i:], '\n')
		if lineEnd < 0 {
			continue // cut short by the end of the read: partial line
		}
		var entry map[string]any
		if json.Unmarshal(buf[lineStart:i+lineEnd], &entry) == nil {
			if t, ok := entry[kind.field].(string); ok && t != "" {
				return t
			}
		}
	}
	return ""
}

// editors are the editors whose files are worth showing, with the options
// of each that take a value, so the value is not taken for a file.
var editors = map[string]string{
	"vim": "-c -S -u -U -i -t -q -T -w -W -s --cmd", "vi": "-c -S -u -U -i -t -q -T -w -W -s --cmd",
	"nvim": "-c -S -u -i -t -q -w -W -s --cmd --listen", "view": "-c -S -u -U -i -t -q -T -w -W -s --cmd",
	"vimdiff": "-c -S -u -U -i -t -q -T -w -W -s --cmd", "nano": "-T -r -Y -s -o -Q -C -X -f",
	"micro": "-config-dir -plugin", "hx": "-c --config --log", "helix": "-c --config --log",
	"emacs": "-l --load -f --funcall --eval", "kak": "-c -e -s -E", "pico": "-T -r -Y -s -o -Q",
	"joe": "", "ne": "", "mcedit": "",
}

// IsEditor is whether command, as tmux names a pane's current command, is
// an editor EditorFiles knows.
func IsEditor(command string) bool {
	_, ok := editors[command]
	return ok
}

// EditorFiles is the files the editor in the foreground of the terminal of
// process shell (a pane's first process) was started on, separated by
// spaces, or "" if there is none, it is not an editor, or this system has
// no /proc.
func EditorFiles(proc string, shell int) string {
	if shell <= 0 || proc == "" {
		return ""
	}
	fg := foreground(proc, shell)
	if fg <= 0 {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(fg), "cmdline"))
	if err != nil {
		return ""
	}
	return Files(strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00"))
}

// foreground is the foreground process group of the terminal of process
// pid, from /proc/PID/stat: its leader is the program in the foreground.
func foreground(proc string, pid int) int {
	raw, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	// pid (comm) state ppid pgrp session tty_nr tpgid ...; comm may
	// contain spaces and parentheses, so fields start after the last ")".
	i := bytes.LastIndexByte(raw, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(string(raw[i+1:]))
	if len(f) < 6 {
		return 0
	}
	n, _ := strconv.Atoi(f[5])
	return n
}

// Files is the files on an editor's command line, args[0] being the
// editor: its arguments that are not options or their values, nor +line
// positions, separated by spaces.
func Files(args []string) string {
	if len(args) == 0 {
		return ""
	}
	takesValue, ok := editors[filepath.Base(args[0])]
	if !ok {
		return ""
	}
	valued := map[string]bool{}
	for _, o := range strings.Fields(takesValue) {
		valued[o] = true
	}
	var files []string
	options := true
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch {
		case options && a == "--":
			options = false
		case options && valued[a]:
			i++ // its value
		case options && (strings.HasPrefix(a, "-") && a != "-" || strings.HasPrefix(a, "+")):
		case a != "":
			files = append(files, a)
		}
	}
	return strings.Join(files, " ")
}
