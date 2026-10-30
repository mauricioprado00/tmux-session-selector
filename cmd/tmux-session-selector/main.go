// Command tmux-session-selector lists tmux sessions, windows and panes as a
// tree that can be filtered by words or a regex, and jumps to the selected one.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
	"github.com/mauricioprado00/tmux-session-selector/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: tmux-session-selector [flags] [initial query]\n\n")
		flag.PrintDefaults()
	}
	client := flag.String("client", "", "tmux client to switch (default: the calling client)")
	regex := flag.Bool("regex", false, "start in regex mode")
	noPreview := flag.Bool("no-preview", false, "start with the preview hidden")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("tmux-session-selector", version)
		return 0
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
