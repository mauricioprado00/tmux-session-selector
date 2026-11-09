package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRead(t *testing.T) {
	dir, proc := t.TempDir(), t.TempDir()
	write := func(path, content string) {
		t.Helper()
		path = filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	alive := func(pid string) {
		if err := os.Mkdir(filepath.Join(proc, pid), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("sessions/10.json", `{"pid":10,"sessionId":"aaaaaaaa-1","name":"fix it","status":"busy","tmux":"my:sess.1:@4.%7"}`)
	write("sessions/11.json", `{"pid":11,"sessionId":"bbbbbbbb-2","name":"ask","status":"waiting","tmux":"w:@5.%8"}`)
	write("sessions/12.json", `{"pid":12,"sessionId":"cccccccc-3","status":"idle"}`)
	write("jobs/cccccccc/state.json", `{"sessionId":"cccccccc-3","state":"working","tempo":"blocked","detail":"which branch?","name":"XBT-1"}`)
	write("sessions/13.json", `{"pid":13,"sessionId":"dddddddd-4","status":"idle"}`)
	write("jobs/dddddddd/state.json", `{"sessionId":"dddddddd-4","state":"done","tempo":"idle"}`)
	write("sessions/14.json", `{"pid":14,"sessionId":"eeeeeeee-5","status":"busy"}`) // gone
	write("sessions/15.json", `not json`)
	for _, pid := range []string{"10", "11", "12", "13", "15"} {
		alive(pid)
	}
	got := Read(dir, proc)
	want := []Session{
		{PID: 10, ID: "aaaaaaaa-1", Name: "fix it", State: Working, TmuxSession: "my:sess.1", Window: "@4", Pane: "%7"},
		{PID: 11, ID: "bbbbbbbb-2", Name: "ask", State: NeedsInput, TmuxSession: "w", Window: "@5", Pane: "%8"},
		{PID: 12, ID: "cccccccc-3", Name: "XBT-1", State: NeedsInput, Job: true, Detail: "which branch?"},
		{PID: 13, ID: "dddddddd-4", State: Idle, Job: true},
	}
	if len(got) != len(want) {
		t.Fatalf("Read = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("session %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestState(t *testing.T) {
	for _, tt := range []struct {
		status string
		j      *job
		want   State
	}{
		{"busy", &job{State: "blocked"}, Working},
		{"waiting", nil, NeedsInput},
		{"idle", nil, Idle},
		{"", nil, None},
		{"waiting", &job{State: "failed"}, Idle},
		{"idle", &job{State: "done", Tempo: "blocked"}, Idle},
		{"idle", &job{State: "working", Tempo: "blocked"}, NeedsInput},
		{"idle", &job{State: "blocked"}, NeedsInput},
		{"idle", &job{State: "working", Tempo: "active"}, Working},
	} {
		if got := state(tt.status, tt.j); got != tt.want {
			t.Errorf("state(%q, %+v) = %v, want %v", tt.status, tt.j, got, tt.want)
		}
	}
}
