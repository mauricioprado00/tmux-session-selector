package claude

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDirs(t *testing.T) {
	root := t.TempDir()
	home, duty, gone := filepath.Join(root, "home"), filepath.Join(root, "duty"), filepath.Join(root, "gone")
	for _, d := range []string{home, duty} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	proc := filepath.Join(root, "proc")
	pid := func(n, comm, env string) {
		d := filepath.Join(proc, n)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "comm"), []byte(comm+"\n"), 0o644)
		os.WriteFile(filepath.Join(d, "environ"), []byte(env), 0o644)
	}
	pid("10", "claude", "A=1\x00CLAUDE_CONFIG_DIR="+duty+"\x00")
	pid("11", "claude", "CLAUDE_CONFIG_DIR="+duty+"/\x00")
	pid("12", "claude", "CLAUDE_CONFIG_DIR="+gone+"\x00")
	pid("13", "bash", "CLAUDE_CONFIG_DIR="+filepath.Join(root, "proc")+"\x00")
	pid("14", "claude", "HOME=/x\x00")

	if got, want := Dirs(proc, home), []string{home, duty}; !slices.Equal(got, want) {
		t.Errorf("Dirs = %v, want %v", got, want)
	}
	if got := Dirs("", home, home); !slices.Equal(got, []string{home}) {
		t.Errorf("Dirs without proc = %v", got)
	}
}
