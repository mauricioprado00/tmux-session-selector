# tmux-session-selector

A replacement for tmux's `prefix` + `w` tree chooser that you filter by typing
plain words or a regular expression, instead of tmux format expressions.

```
> api vim                                         [words]  3/148
──────────────────────────────────────────────────────────────────
 ▾ api  (3 windows, attached)
   ▾ 1: editor
       %4  nvim  ~/www/api
```

One static binary; nothing to install but tmux.

## Install

Download the archive for your system from the
[latest release](https://github.com/mauricioprado00/tmux-session-selector/releases/latest):

```sh
curl -L https://github.com/mauricioprado00/tmux-session-selector/releases/latest/download/tmux-session-selector_linux_amd64.tar.gz \
  | tar -xz -C ~/.local/bin tmux-session-selector
```

Archives exist for `linux_amd64`, `linux_arm64`, `darwin_amd64` and
`darwin_arm64`; `checksums.txt` holds their SHA-256 sums.

Or build and install from a clone with Go 1.22 or newer:

```sh
make install        # into ~/.local/bin
sudo make install   # into /usr/local/bin, for every user
```

`make uninstall` removes it; `BINDIR=…` and `GO=…` override the defaults.

Or with `go install`:

```sh
go install github.com/mauricioprado00/tmux-session-selector/cmd/tmux-session-selector@latest
```

## Bind it

In `~/.tmux.conf` (tmux 3.2 or newer), replacing the built-in chooser:

```tmux
bind w display-popup -E -w 80% -h 80% tmux-session-selector
```

On older tmux, open it in a window instead:

```tmux
bind w new-window -n select tmux-session-selector
```

Reload with `tmux source-file ~/.tmux.conf`.

## Use

Type to filter. `Enter` jumps to the selected session, window or pane; `Esc`
closes without changing anything. The bottom line lists the main keys; `?` opens a
help panel.

The preview below the list draws the selected window's panes in their real
layout and colours, or a session's windows side by side, like tmux's own
chooser. Unlike tmux's snapshot, it is live: output arriving in a pane shows
while you choose.

With tmux's `mouse` option on, the mouse works too: click selects, double-click
jumps, the wheel moves, and clicking a pane or window in the preview jumps
there; right-click opens a menu of commands, as in tmux. When a session has
more windows than fit, its preview scrolls like
tmux's: click the `<` / `>` arrows or use the wheel over it.

| Query | Shows |
|---|---|
| `api vim` | rows containing both words, in any of session, window, command, path or pane title |
| `!zsh` | rows not containing `zsh` |
| `s:api` | sessions whose name contains `api` (and everything in them) |
| `w:edit` | windows whose name or index contains `edit` |
| `c:vim` | panes running a command containing `vim` |
| `d:www` | panes whose current directory contains `www` |
| `/^api-(dev\|prod)$/` | a regular expression (Go RE2 syntax) |

Matching ignores case unless the query has an uppercase letter. A row shown
dimmed does not match itself; it is there because something inside it does.

Windows show tmux's flags after their name: `*` current, `-` last, `#`
activity, `!` bell (in red), `~` silence, `M` marked pane, `Z` zoomed.

| Key | Action |
|---|---|
| `Up` / `Down`, `Ctrl-P` / `Ctrl-N`, `Ctrl-K` / `Ctrl-J` | move |
| `PgUp` / `PgDn`, `Home` / `End` | move further |
| `Right` / `Tab`, `Left` / `Shift-Tab` | expand, collapse |
| `Backspace`, `Ctrl-W`, `Ctrl-U` | delete a character, a word, everything |
| `Ctrl-R` | switch between words and regex mode |
| `Ctrl-V` | show or hide the pane preview |
| `?` (empty query), `F1` | help panel with the query syntax and keys |
| `<` / `>` (empty query) | scroll the windows of a session preview |
| `Enter` | jump |
| `Esc`, `Ctrl-C`, `Ctrl-G` | cancel |

The commands of tmux's own chooser work on the same letters with **Alt**
(plain letters are query text):

| Key | Action |
|---|---|
| `Alt-0` … `Alt-9` | jump to the row with that number on the left |
| `Alt-x` | kill the selected session, window or pane (asks y/n) |
| `Alt-t` / `Alt-T` / `Ctrl-T` | tag or untag the selected row / untag all / tag every match |
| `Alt-m` / `Alt-M` | set / clear tmux's marked pane |
| `Alt-X` | kill the tagged items (or the selected one), asking first |
| `Alt-:` | run a tmux command on each tagged item; `%%` is the target |
| `Alt-o` | sort by index (creation order), name, or time (most recent first) |
| `Alt-r` | reverse the sort |
| `Alt-h` | back to the pane you started from |
| `Alt-+` / `Alt--` | expand / collapse everything |

Command-line flags:

```
tmux-session-selector [flags] [initial query]

  -client NAME   tmux client to switch (default: the calling client)
  -regex         start in regex mode
  -no-preview    start with the preview hidden
  -refresh DUR   how often the preview updates (default 250ms; 0 for a snapshot)
  -command CMD   run a tmux command on the chosen target instead of switching
                 (the first %% and every %1 become the target)
  -sort ORDER    index (default), name or time
  -reverse       reverse the sort order
  -all-groups    show every session of a session group, not just one
  -version       print the version and exit
```

An initial query gives a pre-filtered view, for example
`bind W display-popup -E "tmux-session-selector s:work"`.

Run outside tmux, it attaches to the selected session.

Like `choose-tree`'s template, `-command` runs any tmux command on the chosen
target instead, for example to pull a pane into the current window:

```tmux
bind J display-popup -E "tmux-session-selector -command \"join-pane -h -s '%%'\""
```

## Development

```sh
go test ./...   # the integration tests need tmux; they use their own server
```

Pushing a `v*` tag builds the release archives and publishes a GitHub release.
See [specs.md](specs.md) for the full specification.
