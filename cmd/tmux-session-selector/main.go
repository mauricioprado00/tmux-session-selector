// Command tmux-session-selector lists tmux sessions, windows and panes as a
// tree that can be filtered by words or a regex, and jumps to the selected one.
package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
	"github.com/mauricioprado00/tmux-session-selector/internal/tmuxconf"
	"github.com/mauricioprado00/tmux-session-selector/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

const querySyntax = `
query syntax (every word must match; case is ignored unless the query has
an uppercase letter):

  api vim      rows with both words, in any field
  !zsh         rows without zsh
  s:api        session name contains api
  w:edit       window name or index contains edit
  c:vim        pane command contains vim
  d:www        pane directory contains www
  /regex/      a regular expression (Ctrl-R switches modes)

Press F1 in the selector for the keys.
`

// tmuxBinding adds (bind) or removes the selector's key binding, in the
// configuration file and in the running tmux server, and reports what it did.
// bindingFlags are the options of -tmux-bind and -tmux-unbind.
type bindingFlags struct {
	action      string // both, select or overview
	key         string // for the select binding
	overviewKey string // for the overview binding
	open        string
	config      string
}

// tmuxBinding adds (bind) or removes the selector's key bindings, in the
// configuration file and in the running tmux server, and reports what it did.
func tmuxBinding(bind bool, f bindingFlags, args []string) int {
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "tmux-session-selector:", err)
		return 1
	}
	usage := func(msg string) int {
		fmt.Fprintln(os.Stderr, "tmux-session-selector: "+msg)
		return 2
	}
	actions := tmuxconf.Actions
	switch {
	case f.action == "both":
	case slices.Contains(tmuxconf.Actions, f.action):
		actions = []string{f.action}
	default:
		return usage("-action must be both, " + strings.Join(tmuxconf.Actions, " or "))
	}
	if f.config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fail(err)
		}
		f.config = tmuxconf.DefaultPath(home, os.Getenv("XDG_CONFIG_HOME"))
	}
	tm := tmux.Tmux{}
	if !bind {
		for _, action := range actions {
			removed, live, err := tmuxconf.Uninstall(tm, f.config, action)
			switch {
			case err != nil:
				return fail(err)
			case removed == nil:
				fmt.Printf("no %s binding in %s\n", action, f.config)
			default:
				fmt.Printf("removed the %s binding of prefix + %s from %s\n", action, removed.Key, f.config)
				if live {
					fmt.Printf("  and from the running tmux server, restoring: %s\n", orNothing(removed.Previous))
				}
			}
		}
		return 0
	}
	keys := map[string]string{tmuxconf.Select: f.key, tmuxconf.Overview: f.overviewKey}
	for _, action := range actions {
		if k := keys[action]; k == "" || strings.ContainsAny(k, " \t\n") {
			return usage("-key and -overview-key must be tmux key names, such as w, O or C-s")
		}
	}
	if len(actions) == 2 && f.key == f.overviewKey {
		return usage("-key and -overview-key must differ")
	}
	if !slices.Contains(tmuxconf.Opens, f.open) {
		return usage("-open must be one of " + strings.Join(tmuxconf.Opens, ", "))
	}
	self, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	version, _ := tm.Run("-V")
	open, note := tmuxconf.Supported(f.open, version)
	if note != "" {
		fmt.Printf("%s: %s.\n", strings.TrimSpace(version), note)
	}
	anyLive := false
	for _, action := range actions {
		argv := []string{self}
		if action == tmuxconf.Overview {
			argv = append(argv, "-overview")
		}
		b := tmuxconf.Binding{Action: action, Key: keys[action], Argv: append(argv, args...),
			Open: open, Border: note != "" && open == tmuxconf.Full}
		live, err := tmuxconf.Install(tm, f.config, b)
		if err != nil {
			return fail(err)
		}
		anyLive = anyLive || live
		fmt.Printf("bound prefix + %s to the %s in %s:\n  %s\n", b.Key, map[string]string{
			tmuxconf.Select: "selector", tmuxconf.Overview: "overview"}[action], f.config, b.Line())
	}
	if anyLive {
		fmt.Println("Active in the running tmux server now.")
	} else {
		fmt.Println("No tmux server is running; the bindings take effect when tmux starts.")
	}
	return 0
}

func orNothing(s string) string {
	if s == "" {
		return "nothing (the key was unbound)"
	}
	return s
}

func main() {
	os.Exit(run())
}

func run() int {
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "usage: tmux-session-selector [flags] [initial query]\n\n")
		flag.PrintDefaults()
		fmt.Fprint(out, querySyntax)
	}
	client := flag.String("client", "", "tmux client to switch (default: the calling client)")
	regex := flag.Bool("regex", false, "start in regex mode")
	normal := flag.Bool("normal", false, "start in normal mode, where keys are commands as in tmux's chooser")
	noPreview := flag.Bool("no-preview", false, "start with the preview hidden, whatever it was left at last time")
	command := flag.String("command", "", "tmux command to run on the chosen target instead of switching to it;\nthe first %% and every %1 become the target, e.g. \"join-pane -s '%%'\"")
	sortOrder := flag.String("sort", "index", "sort order: "+strings.Join(ui.SortOrders, ", "))
	reverse := flag.Bool("reverse", false, "reverse the sort order")
	zoom := flag.Bool("zoom", false, "zoom the pane the selector runs in while it runs (not in a popup)")
	allGroups := flag.Bool("all-groups", false, "show every session of a session group, not just one")
	refresh := flag.Duration("refresh", 250*time.Millisecond, "how often the preview updates; 0 for a single snapshot")
	showVersion := flag.Bool("version", false, "print the version and exit")
	bind := flag.Bool("tmux-bind", false, "bind the selector to a key in tmux (see -key and -config)\nand exit; arguments after -- go into the bound command")
	unbind := flag.Bool("tmux-unbind", false, "remove the binding -tmux-bind added, and exit")
	action := flag.String("action", "both", "with -tmux-bind or -tmux-unbind: both, select (the list) or overview")
	key := flag.String("key", "w", "with -tmux-bind: the selector's key, after tmux's prefix; w replaces tmux's chooser")
	overviewKey := flag.String("overview-key", "O", "with -tmux-bind: the overview's key, after tmux's prefix")
	overview := flag.Bool("overview", false, "open straight into the overview of the current session")
	open := flag.String("open", "full", "with -tmux-bind: full (a borderless popup covering the whole terminal),\npopup (80% over the current window) or window (a new tmux window)")
	config := flag.String("config", "", "with -tmux-bind or -tmux-unbind: the tmux configuration file\n(default: the one tmux reads, such as ~/.tmux.conf)")
	flag.Parse()

	if *showVersion {
		fmt.Println("tmux-session-selector", version)
		return 0
	}
	if *bind || *unbind {
		return tmuxBinding(*bind, bindingFlags{action: *action, key: *key, overviewKey: *overviewKey,
			open: *open, config: *config}, flag.Args())
	}
	if !slices.Contains(ui.SortOrders, *sortOrder) {
		fmt.Fprintf(os.Stderr, "tmux-session-selector: -sort must be one of %s\n", strings.Join(ui.SortOrders, ", "))
		return 2
	}
	// A binding whose command tmux did not format-expand passes the format
	// itself; fall back to the calling client.
	if strings.Contains(*client, "#{") {
		*client = ""
	}

	tm := tmux.Tmux{}
	inside := os.Getenv("TMUX") != "" || *client != ""
	target, err := ui.Run(ui.Options{
		Tmux:      tm,
		Client:    *client,
		Inside:    inside,
		Query:     strings.Join(flag.Args(), " "),
		Regex:     *regex,
		NoPreview: *noPreview,
		StateFile: ui.DefaultStateFile(),
		Refresh:   *refresh,
		Command:   *command,
		Sort:      *sortOrder,
		Reverse:   *reverse,
		AllGroups: *allGroups,
		Zoom:      *zoom,
		Normal:    *normal,
		Overview:  *overview,
	})
	if err == nil && target != nil {
		err = tm.Attach(*target)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tmux-session-selector:", err)
		// Likely launched from a key binding: keep the popup or window open
		// so the message can be read.
		if inside {
			ui.WaitKey("press any key to close")
		}
		return 1
	}
	return 0
}
