// Package jobs finds the background processes of a shell: the jobs stopped
// with Ctrl-Z, or sent to the background with bg or &, as opposed to the
// one in the foreground of the terminal.
package jobs

import (
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Job is a background process, with the background processes it started.
type Job struct {
	PID      int
	Stopped  bool   // stopped (Ctrl-Z), rather than running in the background
	Args     string // its command line
	Children []*Job
}

// List returns the background processes under each of shells, keyed by the
// shell's pid. It runs ps once.
func List(shells []int) (map[int][]*Job, error) {
	out, err := exec.Command("ps", "-A", "-ww", "-o", "pid=,ppid=,pgid=,tpgid=,stat=,args=").Output()
	if err != nil {
		return nil, err
	}
	return Parse(string(out), shells), nil
}

type proc struct {
	pid, ppid, pgid, tpgid int
	stat, args             string
}

// Parse finds the background processes under each of shells in the output
// of ps -o pid=,ppid=,pgid=,tpgid=,stat=,args=.
//
// A process is in the background when it has a controlling terminal but is
// not in the terminal's foreground process group, and is not in the shell's
// own group either: the shell's group is out of the foreground whenever a
// job is in it. Each background process is put under its nearest background
// ancestor, so a job shows as the tree of its processes.
func Parse(out string, shells []int) map[int][]*Job {
	procs := map[int]*proc{}
	children := map[int][]*proc{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		var n [4]int
		ok := true
		for i := range n {
			v, err := strconv.Atoi(f[i])
			n[i], ok = v, ok && err == nil
		}
		if !ok {
			continue
		}
		p := &proc{pid: n[0], ppid: n[1], pgid: n[2], tpgid: n[3], stat: f[4], args: strings.Join(f[5:], " ")}
		procs[p.pid] = p
		children[p.ppid] = append(children[p.ppid], p)
	}
	for _, c := range children {
		sort.Slice(c, func(i, j int) bool { return c[i].pid < c[j].pid })
	}
	found := map[int][]*Job{}
	for _, shell := range shells {
		sh := procs[shell]
		if sh == nil {
			continue
		}
		var walk func(pid int, under *[]*Job, depth int)
		walk = func(pid int, under *[]*Job, depth int) {
			if depth > 64 { // ps output is a snapshot; never trust it to be a tree
				return
			}
			for _, c := range children[pid] {
				into := under
				if c.tpgid > 0 && c.pgid != c.tpgid && c.pgid != sh.pgid {
					j := &Job{PID: c.pid, Stopped: strings.HasPrefix(c.stat, "T"), Args: c.args}
					*under = append(*under, j)
					into = &j.Children
				}
				walk(c.pid, into, depth+1)
			}
		}
		var jobs []*Job
		walk(shell, &jobs, 0)
		if len(jobs) > 0 {
			found[shell] = jobs
		}
	}
	return found
}
