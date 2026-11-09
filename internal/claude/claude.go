// Package claude reads what Claude Code sessions are doing: whether one
// needs input, is working, or is idle, and which tmux pane it runs in.
//
// Claude Code keeps a file per running process, ~/.claude/sessions/PID.json,
// with the session's id, its name, its status (busy, waiting or idle) and,
// when it runs in tmux, where ("session:@window.%pane"). A background
// session, run by Claude Code's daemon rather than in a pane, also has
// ~/.claude/jobs/ID/state.json (ID being the first 8 characters of the
// session id), whose state (working, blocked, done, failed ...) and tempo
// (active, blocked, idle) feed the Needs input / Working / Completed view of
// `claude agents`. State works the session out from both, as that view does.
// A pane's session can park its conversation as a background job
// (parkedJobId) and then shows that job: the pane takes the job's state.
package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// State is what a session is doing, by how much it wants attention: the
// higher, the more.
type State int

const (
	None       State = iota
	Idle             // finished, or waiting for a new prompt
	Working          // busy
	NeedsInput       // asking a question, or for a permission or decision
)

func (s State) String() string {
	switch s {
	case Idle:
		return "idle"
	case Working:
		return "working"
	case NeedsInput:
		return "needs input"
	}
	return ""
}

// Session is a running Claude Code session.
type Session struct {
	PID   int
	ID    string // the session id
	Name  string
	State State
	// Job is whether it is a background session, with a job's state;
	// Detail is what it last said it was doing.
	Job    bool
	Detail string
	// Where it runs in tmux, if it does: the session's name, the window and
	// the pane ids.
	TmuxSession, Window, Pane string
}

// Read lists the running sessions under Claude Code's directory dir,
// sorted by process id. proc is /proc, to leave out the files of processes
// that have gone; "" skips that check.
//
// A session in a pane can park its conversation as a background job
// (parkedJobId) and go on showing that job, under the job's name: the pane
// then has the job's state, and the job is not listed again on its own.
func Read(dir, proc string) []Session {
	if dir == "" {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(dir, "sessions", "*.json"))
	type file struct {
		pid         int
		SessionID   string `json:"sessionId"`
		Name        string `json:"name"`
		Status      string `json:"status"`
		Tmux        string `json:"tmux"`
		JobID       string `json:"jobId"`
		ParkedJobID string `json:"parkedJobId"`
	}
	var all []file
	byJob := map[string]int{} // job id to its process, in all
	for _, f := range files {
		pid, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(f), ".json"))
		if err != nil || pid <= 0 {
			continue
		}
		if proc != "" {
			if _, err := os.Stat(filepath.Join(proc, strconv.Itoa(pid))); err != nil {
				continue
			}
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s file
		if json.Unmarshal(data, &s) != nil || s.SessionID == "" {
			continue
		}
		s.pid = pid
		if s.JobID == "" && len(s.SessionID) >= 8 && s.Tmux == "" {
			s.JobID = s.SessionID[:8]
		}
		if s.JobID != "" {
			byJob[s.JobID] = len(all)
		}
		all = append(all, s)
	}
	parked := map[string]bool{}
	for _, s := range all {
		if s.Tmux != "" && s.ParkedJobID != "" {
			parked[s.ParkedJobID] = true
		}
	}
	var out []Session
	for _, s := range all {
		if s.Tmux == "" && parked[s.JobID] {
			continue // shown in its pane
		}
		ses := Session{PID: s.pid, ID: s.SessionID, Name: s.Name}
		ses.TmuxSession, ses.Window, ses.Pane = parseTmux(s.Tmux)
		status, jobID, jobSession := s.Status, s.JobID, s.SessionID
		if s.Tmux != "" && s.ParkedJobID != "" {
			// The pane shows the parked job: the status of the job's
			// process, if it runs, and the job's state.
			jobID, jobSession, status = s.ParkedJobID, "", ""
			if i, ok := byJob[jobID]; ok {
				ses.ID, ses.Name, status, jobSession = all[i].SessionID, all[i].Name, all[i].Status, all[i].SessionID
			}
		}
		if j := readJob(dir, jobID, jobSession); j != nil {
			ses.Job, ses.Detail = true, j.Detail
			if j.Name != "" {
				ses.Name = j.Name
			}
			ses.State = state(status, j)
		} else {
			if status == "" {
				status = s.Status // a parked job gone without a trace: the pane's own
			}
			ses.State = state(status, nil)
		}
		out = append(out, ses)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// job is the part of a background session's state.json that matters here.
type job struct {
	SessionID string `json:"sessionId"`
	State     string `json:"state"`
	Tempo     string `json:"tempo"`
	Detail    string `json:"detail"`
	Name      string `json:"name"`
}

// readJob reads the state of job id, which runs session ("" if not known).
func readJob(dir, id, session string) *job {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "jobs", id, "state.json"))
	if err != nil {
		return nil
	}
	var j job
	if json.Unmarshal(data, &j) != nil || session != "" && j.SessionID != "" && j.SessionID != session {
		return nil
	}
	return &j
}

// state works out a session's state from its process's status and, for a
// background session, its job, in the order `claude agents` does: busy is
// working; a job that failed or stopped is done; waiting needs input; a job
// done is done; a job blocked needs input; any other job is working.
func state(status string, j *job) State {
	switch {
	case status == "busy":
		return Working
	case j != nil && (j.State == "failed" || j.State == "stopped" || j.State == "crashed"):
		return Idle
	case status == "waiting":
		return NeedsInput
	case j != nil && (j.State == "done" || j.State == "success"):
		return Idle
	case j != nil && (j.Tempo == "blocked" || j.State == "blocked"):
		return NeedsInput
	case j != nil:
		return Working
	case status == "idle":
		return Idle
	}
	return None
}

// parseTmux splits "session:@window.%pane"; the session's name may hold
// colons and dots itself.
func parseTmux(s string) (session, window, pane string) {
	i := strings.LastIndex(s, ":@")
	if i < 0 {
		return "", "", ""
	}
	session, rest := s[:i], s[i+1:]
	window, pane, ok := strings.Cut(rest, ".%")
	if !ok {
		return "", "", ""
	}
	return session, window, "%" + pane
}
