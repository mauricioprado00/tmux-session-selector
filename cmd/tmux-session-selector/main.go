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
	noPreview := flag.Bool("no-preview", false, "start with the preview hidden")
	sortOrder := flag.String("sort", "index", "sort order: "+strings.Join(ui.SortOrders, ", "))
	reverse := flag.Bool("reverse", false, "reverse the sort order")
	refresh := flag.Duration("refresh", 250*time.Millisecond, "how often the preview updates; 0 for a single snapshot")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("tmux-session-selector", version)
		return 0
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
		Refresh:   *refresh,
		Sort:      *sortOrder,
		Reverse:   *reverse,
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
