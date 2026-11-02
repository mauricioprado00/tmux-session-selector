package tmux

import (
	"bufio"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Control is a control-mode client (tmux -C) attached to one session, so
// tmux tells it what happens there as it happens: which panes print, and
// when windows open, close, are renamed or laid out again, or a pane's
// title, directory or command changes. It is read-only, and tmux leaves it
// out when sizing windows.
//
// News gets a value whenever there is something new to Take; news that
// comes in faster than it is taken is merged, so a busy pane costs nothing
// beyond its first line of output.
type Control struct {
	News <-chan struct{}
	news chan struct{}

	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan struct{} // closed once tmux's output has ended

	mu      sync.Mutex
	panes   map[string]bool
	windows bool
	exited  bool
}

// subscription asks tmux to report changes to every pane's title,
// directory and command, and its window's flags; tmux checks once a second.
// Output and window changes come without asking.
const subscription = "refresh-client -B 'tss:%*:#{pane_title}|#{pane_current_path}|#{pane_current_command}|#{window_flags}'\n"

// Control attaches a control-mode client to session (an id such as $1).
// tmux counts it as attached and sets the session's activity time to now,
// as for any client that attaches.
func (t Tmux) Control(session string) (*Control, error) {
	args := append(append([]string{}, t.Args...), "-C", "attach-session", "-t", session, "-f", "ignore-size,read-only")
	cmd := exec.Command("tmux", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := newControl()
	c.cmd, c.stdin = cmd, stdin
	io.WriteString(stdin, subscription)
	go c.read(stdout)
	return c, nil
}

func newControl() *Control {
	news := make(chan struct{}, 1)
	return &Control{News: news, news: news, done: make(chan struct{}), panes: map[string]bool{}}
}

// Take returns what has happened since the last Take: the panes that
// printed, whether windows or panes changed in other ways, and whether the
// client has gone (the session closed, the server stopped, or tmux could
// not attach), after which nothing more comes.
func (c *Control) Take() (panes map[string]bool, windows, exited bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	panes, windows, exited = c.panes, c.windows, c.exited
	c.panes, c.windows = map[string]bool{}, false
	return panes, windows, exited
}

// Close detaches the client and waits for it to go, so that tmux no longer
// counts it when it picks a client for a command.
func (c *Control) Close() {
	c.stdin.Close() // tmux -C detaches at the end of its input
	select {
	case <-c.done:
	case <-time.After(time.Second):
		c.cmd.Process.Kill()
		<-c.done
	}
	c.cmd.Wait()
}

// read reads tmux's notifications until its output ends. Only the start of
// a line matters, so the rest of a long %output line is skipped unread.
func (c *Control) read(r io.Reader) {
	defer close(c.done)
	br := bufio.NewReaderSize(r, 4096)
	block := false
	for {
		line, err := br.ReadSlice('\n')
		head := string(line[:min(len(line), 256)])
		for err == bufio.ErrBufferFull {
			_, err = br.ReadSlice('\n')
		}
		if head != "" {
			c.note(head, &block)
		}
		if err != nil {
			break
		}
	}
	c.mu.Lock()
	c.exited = true
	c.mu.Unlock()
	c.signal()
}

// note takes in one line of tmux's output. block is whether the line is
// inside the reply to a command, between %begin and %end or %error, which
// holds no notifications.
func (c *Control) note(line string, block *bool) {
	line = strings.TrimRight(line, "\r\n")
	if *block {
		*block = !strings.HasPrefix(line, "%end ") && !strings.HasPrefix(line, "%error ")
		return
	}
	kind, rest, _ := strings.Cut(line, " ")
	c.mu.Lock()
	switch kind {
	case "%begin":
		*block = true
		c.mu.Unlock()
		return
	case "%output", "%extended-output":
		pane, _, _ := strings.Cut(rest, " ")
		c.panes[pane] = true
	case "%layout-change", "%window-add", "%window-close", "%window-renamed", "%window-pane-changed",
		"%session-window-changed", "%session-renamed", "%subscription-changed", "%pane-mode-changed":
		c.windows = true
	case "%exit":
		c.exited = true
	default:
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.signal()
}

// signal tells whoever waits on News that there is news, unless it has been
// told already.
func (c *Control) signal() {
	select {
	case c.news <- struct{}{}:
	default:
	}
}
