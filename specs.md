# tmux-session-selector — Specification

## 1. Background

tmux's built-in tree chooser (`prefix` + `w`, i.e. `choose-tree`) is good for a
handful of sessions, but with many sessions, windows and panes it becomes hard
to jump to the one you want. Its filter (`f`) takes a tmux *format expression*
such as `#{m:*api*,#{session_name}}`, which is not something a person wants to
type in the middle of work.

This project is a drop-in replacement for that chooser: the same tree of
sessions → windows → panes, but filtered live by typing plain words or a regular
expression.

## 2. Goals

- Show every session, window and pane on the tmux server as a tree, like
  `choose-tree`.
- Filter the tree as you type, using plain words by default and a regex when
  asked.
- Jump to the selected session, window or pane with Enter.
- Open with a single key binding, ideally replacing `prefix` + `w`.
- Ship as one static binary with no runtime dependencies other than tmux.
- Build and publish releases automatically from GitHub Actions.

## 3. Non-goals (v1)

- Fuzzy matching. Matching is by substring or regex, which is predictable;
  anyone who wants fuzzy matching already has `fzf`.
- Creating, renaming or reorganising sessions and windows.
- A configuration file. Defaults first; options come as CLI flags if needed.
- Windows (the OS). tmux runs on Linux and macOS only.

## 4. Implementation constraints

| Item | Decision |
|---|---|
| Language | **Go**: a single static binary, simple cross-compilation, small toolchain. Python (needs an interpreter on every host) and Rust (heavier toolchain and dependency tree) were the alternatives. |
| Dependencies | Go standard library only. Raw mode and terminal size use `ioctl` through `syscall`, with the request constants in per-OS files (Linux; macOS and the BSDs). No TUI framework; the screen is drawn with plain ANSI escape sequences. Any dependency needs a stated reason. |
| tmux interaction | Only by running the `tmux` binary (`list-panes`, `capture-pane`, `switch-client`, …). No parsing of tmux internals or sockets. |
| Minimum tmux | 3.2 for popup mode (`display-popup`). Older versions work through the fallback launch described in §9. |
| Binary name | `tmux-session-selector` |

## 5. Data model

All data comes from a single call:

```
tmux list-panes -a -F '<format>'
```

with fields separated by `\x1f` (the ASCII unit separator). tmux 3.x does not
print control characters raw: it writes them as octal escapes (`\037`) and a
backslash as `\\`, so the parser decodes those escapes while splitting. A raw
`\x1f` (older tmux) is accepted too. Fields read per pane:

| Level | Fields |
|---|---|
| Session | `session_id`, `session_name`, `session_attached`, `session_activity` |
| Window | `window_id`, `window_index`, `window_name`, `window_active`, `window_width`, `window_height`, `window_zoomed_flag`, `window_raw_flags`, `window_activity` |
| Pane | `pane_id`, `pane_index`, `pane_active`, `pane_current_command`, `pane_current_path`, `pane_title`, `pane_left`, `pane_top`, `pane_width`, `pane_height`, `cursor_x`, `cursor_y`, `pane_marked` |

The geometry and cursor fields are only used to draw the preview (§6.4).

The current location (`client_name`, `session_id`, `window_id`, `pane_id`) is
read with `display-message -p` for the calling client, or from `list-clients`
when `--client` names one. It is used to mark and pre-select the current row.

A window linked into several sessions appears under each of them.

Sessions in a session group share their windows, so, like tmux's chooser,
only one session of each group is shown unless `--all-groups` (tmux's `-G`)
is given: the session the client is in, if it is in the group, else the
group's first (lowest id). Its row says `group NAME: a,b`. Fields read for
this: `session_group`, `session_group_list`.

Rows are grouped into a tree: session → window → pane. Windows with a single
pane do not show a pane row; the window row stands for that pane.

The tree is sorted as tmux's chooser sorts it (`-O` / `-r` there):

| Order | Sessions | Windows |
|---|---|---|
| `index` (default) | by id, i.e. creation order | by index |
| `name` | by name | by name |
| `time` | most recent `session_activity` first | most recent `window_activity` first |

Panes are always in index order. Reversing applies to every level. The prompt
line shows the order, e.g. `(sort: time, reversed)`, as tmux's does.

## 6. User interface

### 6.1 Layout

```
 > api vim                                         [words]  12/148
 ──────────────────────────────────────────────────────────────────
 ▾ api  (3 windows, attached)
   ▾ 1: editor
       %4  nvim       ~/www/api
       %5  zsh        ~/www/api
     2: server        go          ~/www/api
 ▸ blog  (2 windows)
┌─ %4 nvim ───────────────────┬─ %5 zsh ─────────────────────────┐
│<pane content, in colour>    │<pane content>                    │
└─────────────────────────────┴──────────────────────────────────┘
 Enter jump  ↑↓ move  →← expand/collapse  ^R regex  ^V preview  Esc quit  ? help
```

- **Prompt line**: the query, the matching mode (`words` or `regex`), and the
  count of matching rows against the total. While the query is empty it shows
  a dimmed hint, `type to filter · ? for help`.
- **Tree**: one row per session, window and pane.
  - Session row: name, window count, `attached` when a client is attached.
  - Window row: `index: name` followed by tmux's window flags
    (`window_raw_flags`: `*` current, `-` last, `#` activity, `!` bell,
    `~` silence, `M` marked pane, `Z` zoomed), with `!` in red and `#` / `~`
    in yellow; then `(N panes)` for multi-pane windows, or the command and
    path for single-pane ones.
  - Pane row: pane id (followed by `M` when it is the marked pane), current
    command, current path (with `$HOME` shown as `~`).
  - The client's current session, window and pane are marked (bold or `*`).
  - Rows shown only as context for a match (see §7.4) are dimmed; the matched
    text is highlighted.
- **Preview** (toggle, on by default when the terminal has at least 30 lines):
  the lower half of the screen; see §6.4.
- **Footer**: the bottom line always lists the main keys and `? help`, so every
  shortcut can be found without reading documentation.
- Long rows are truncated with `…`; the screen never wraps.
- The UI redraws on terminal resize (`SIGWINCH`).

### 6.2 Help panel

`?` (while the query is empty) or `F1` replaces the tree and preview with a
help panel: the query syntax (words, `!`, `s:`, `w:`, `c:`, `d:`, `/regex/`,
smart case) and every key binding. Any key closes it; keys other than `Esc`,
`Enter` and `F1` also take effect, so typing goes straight into the query.
`F1` alone is not enough because some terminals (GNOME Terminal) keep it for
their own help. The same query syntax is printed by `--help`.

### 6.3 Initial state

- Query empty, all sessions expanded to window level, the current window
  selected.
- Mode `words`.

### 6.4 Preview

Modelled on tmux's own `choose-tree` preview:

- **Pane row**: that pane, framed, using the whole preview area.
- **Window row**: every pane of the window in its real layout, scaled to the
  preview area, with box-drawn borders that meet in proper junctions. Each pane
  is labelled with its id and command on its top border; the active pane's
  border is green. A zoomed window shows only its zoomed pane.
- **Session row**: the session's windows side by side, each drawn with its own
  layout and labelled `index: name`; the active window's frame is green. When
  the windows do not fit at 24 columns each (tmux's own threshold), the
  preview shows as many as fit at that width and scrolls, as in tmux:
  framed `<` / `>` arrow columns at the edges mark hidden windows, and a newly
  shown session starts with its active window in the middle. Clicking an arrow,
  the mouse wheel over the preview, or `<` / `>` on an empty query scroll by
  one window. Only the windows on screen are captured.
- Content keeps its colours and attributes: it is read with
  `capture-pane -p -e` and its SGR sequences are parsed into per-cell styles
  (other escape sequences are dropped).
- A pane larger than its box shows its top-left part, scrolled just enough to
  keep the cursor visible, as tmux does.
- All panes needed for one preview are captured in a single tmux invocation
  (`capture-pane … ; display-message <marker> <cursor> ; …`); the marker line
  also carries each pane's current cursor position.
- **Live**: unlike tmux's snapshot, the preview is captured again every 250 ms
  (`--refresh`), so output arriving in a pane shows while you choose. Only the
  panes on screen are captured, in one tmux call per tick, and nothing is
  captured while the preview is hidden or help is open. `--refresh 0` keeps
  the first capture of each pane for the whole run. Pane layout is read once
  at start-up; only content and cursor are live.

### 6.5 Key bindings

| Key | Action |
|---|---|
| any printable character | append to the query |
| `Backspace` | delete the previous character |
| `Ctrl-W` | delete the previous word |
| `Ctrl-U` | clear the query |
| `Up` / `Ctrl-P` / `Ctrl-K` | move the selection up |
| `Down` / `Ctrl-N` / `Ctrl-J` | move the selection down |
| `PgUp` / `PgDn` | move by one screen |
| `Home` / `End` | first / last row |
| `Right` / `Tab` | expand the selected row |
| `Left` / `Shift-Tab` | collapse the selected row (or move to its parent) |
| `Ctrl-R` | toggle between `words` and `regex` mode |
| `Ctrl-V` | toggle the preview |
| `?` (empty query) / `F1` | open the help panel |
| `<` / `>` (empty query) | scroll the windows of a session preview |
| `Enter` | jump to the selected target and exit |
| `Esc` / `Ctrl-C` / `Ctrl-G` | exit without changing anything |

Letters are always query input, never commands, so typing never triggers an
action by accident. `?` opens help only when the query is empty; anywhere
else it is query text; the same holds for `<` and `>`.

### 6.6 Mouse

The program turns on mouse reporting (`?1000h`, SGR format `?1006h`) and
turns it off on exit. It behaves like tmux's chooser:

| Action | Effect |
|---|---|
| click a row | select it |
| double-click a row (within 400 ms) | jump to it, like `Enter` |
| click a `▸` / `▾` arrow | expand or collapse the row (empty query only) |
| wheel | move the selection; over a session preview, scroll its windows |
| click a pane box in a window preview, or a window box in a session preview | jump to that pane or window |
| click a `<` / `>` arrow of a session preview | scroll its windows by one |
| click the footer | open the help panel |
| right-click a row | select it and open a menu of commands (below) |

Modifier keys are ignored; the middle button does nothing.

The right-click menu has the items of tmux's chooser menu: Select,
Expand (or Collapse), Mark, Tag (or Untag), Tag All, Tag None, Kill,
Kill Tagged, Cancel. Each shows the key that does the same without the menu.
It opens at the pointer, moved to fit on screen, and is drawn over the list
and preview. While it is open: a click on an item, `Up`/`Down` and `Enter`,
or tmux's letter for the item (`t`, `T`, `a`, `m`, `x`, `X`, `q`, `l`/`h`)
picks it; any other key or click closes it without acting or typing. In a popup or pane,
tmux forwards mouse events only when its `mouse` option is on (checked with
tmux 3.4, in both a window and a `display-popup`).

### 6.7 Commands

The commands of tmux's chooser (`choose-tree`) are available on the same
letters pressed with **Alt**, since plain letters and digits are query text.
A terminal sends Alt-x as `ESC x`; `ESC O` not followed by a known SS3 key is
Alt-O.

| Key | tmux | Action |
|---|---|---|
| `Alt-x` | `x` | Kill the selected session, window or pane, after a `y/n` question on the prompt line (`kill-session`, `kill-window` or `kill-pane`). The tree is reloaded and the selection stays at the same position. |
| `Alt-t` | `t` | Tag or untag the selected row, then move down. Tagged rows are shown in bold magenta and the prompt line counts them. Tags are kept by target, so they survive reloads. |
| `Alt-T` | `T` | Untag everything. |
| `Ctrl-T` | `C-t` | Tag every row that matches the query (context rows are not tagged). |
| `Alt-m` | `m` | Make the selected pane (the active pane of a selected window or session) tmux's marked pane (`select-pane -m`), the default source for `join-pane`, `swap-pane` and `move-pane`. The tree reloads so the `M` flags update. |
| `Alt-M` | `M` | Clear the marked pane (`select-pane -M`). |
| `Alt-X` | `X` | Kill every tagged item, after a `y/n` question; panes go first, then windows, then sessions, so none has gone before its turn. With nothing tagged, the selected row. |
| `Alt-:` (or `Alt-;`) | `:` | Read a tmux command on the prompt line and run it on every tagged item (or the selected row if none), with `%%` and `%1` replaced by each target as in §8.1. `Esc` cancels. The tree is reloaded afterwards. |
| `Alt-o` (or `Alt-O`) | `O` | Change the sort order: index → name → time (§5). |
| `Alt-r` | `r` | Reverse the sort order. |
| `Alt-h` (or `Alt-H`) | `H` | Select the pane the selector was started from, expanding its window, and clearing the query if it hides that pane. Does nothing outside tmux. |
| `Alt-0` … `Alt-9` | `0` … `9` | Jump straight to the row labelled with that digit. The labels are a dim gutter on the left of the first ten rows on screen (tmux numbers from the top of the whole list and continues with `M-a`…, but Alt-letters are the commands here). After filtering, `Alt-0` is the first row shown. |
| `Alt-+` (or `Alt-=`) / `Alt--` | `M-+` / `M--` | Expand / collapse every session and window. The selection moves to the nearest visible ancestor if it is hidden. |

While a question is shown, `y` or `Y` confirms and any other key cancels.

## 7. Filtering

### 7.1 Searchable text

Each row is matched against a *haystack* that includes its own fields and those
of its ancestors:

- Session: `session_name`
- Window: session name + `window_name` + `window_index`
- Pane: session name + window name + `pane_current_command` +
  `pane_current_path` + `pane_title`

Because ancestors are included, `api vim` finds the `vim` pane inside the `api`
session even though no single field contains both words.

### 7.2 Words mode (default)

- The query is split on whitespace into terms.
- A row matches when **every** term is a substring of its haystack (AND).
- **Smart case**: case-insensitive unless the query contains an uppercase
  letter.
- `!term` excludes rows whose haystack contains `term`.
- A field prefix restricts a term to one field:
  - `s:term`: session name
  - `w:term`: window name
  - `c:term`: pane command
  - `d:term`: pane path
  - `!` combines with a prefix: `!c:zsh`.
- No other characters are special. `.`, `*`, `/` and so on match themselves.

### 7.3 Regex mode

- Toggled with `Ctrl-R`, or entered directly by typing a query that starts and
  ends with `/` (for example `/^api-(dev|prod)$/`).
- The whole query is one Go RE2 regular expression, matched against each field
  of the haystack separately (so `^` and `$` anchor to a field, not to the joined
  text).
- Smart case applies the same way; an explicit `(?i)` overrides it.
- An invalid regex does not clear the list: the prompt line shows the error and
  the last valid result stays on screen.

### 7.4 What is shown

- Empty query: the full tree (respecting expand/collapse).
- Non-empty query: a row is shown if it matches **or** any of its descendants
  matches. Ancestors that are shown only because of a matching descendant are
  dimmed. Collapsed state is ignored while a query is active, so every match is
  visible.
- When the rows change, the selection stays on the same row if it is still shown
  and matches. Otherwise it moves to that row's nearest shown ancestor (so
  clearing the query returns to where you were), and failing that to the first
  matching row. A row shown only as context is never kept selected, so `Enter`
  after typing goes to a match, not to its session.
- When nothing matches, the tree area shows `no matches`, and `Enter` does
  nothing.

### 7.5 Performance

Filtering and redrawing must finish within one frame (≈16 ms) for 500 panes on
an ordinary laptop. The tmux data is read once at start-up, not on each
keystroke.

## 8. Jumping to the target

On `Enter`, depending on the selected row:

| Row | Commands |
|---|---|
| Session | `switch-client -t <session_id>` |
| Window | `select-window -t <window_id>` ; `switch-client -t <session_id>` |
| Pane | `select-window -t <window_id>` ; `select-pane -t <pane_id>` ; `switch-client -t <session_id>` |

The commands are sent as one tmux invocation, separated by `;` arguments, so
the switch is atomic from the user's point of view. The window and pane are
selected before switching so the client never shows the old window of the
target session in between. IDs (`$1`, `@3`, `%7`) are used rather
than names, so names containing `:`, `.` or spaces cannot be misparsed.

`switch-client` takes `-c <client>` when `--client` is given (see §9), so the
correct client switches even when several are attached.

When run **outside** tmux (`$TMUX` unset), the tool uses
`attach-session -t <target>` instead, replacing its own process with tmux
(`exec`).

### 8.1 Command template

`--command` replaces the jump with any tmux command, as `choose-tree`'s
`template` argument does. The first `%%` and every `%1` in it become the
chosen target, written with ids (`$1`, `$1:@3` or `$1:@3.%7`). The line is
passed to `tmux source-file -` on standard input, so it is parsed with tmux's
own quoting and may hold several commands separated by `;`. For example:

```tmux
bind J display-popup -E "tmux-session-selector -command \"join-pane -h -s '%%'\""
```

Every way of choosing (Enter, double-click, Alt-digit, a click in the
preview) runs the command, inside or outside tmux. If it fails, its error is
shown on the prompt line, the list is reloaded and the selector stays open.

## 9. Launching

### 9.1 Key binding (recommended)

In `~/.tmux.conf`, replacing the built-in chooser:

```tmux
bind w display-popup -E -w 80% -h 80% tmux-session-selector
```

`-E` closes the popup when the program exits.

`display-popup` and `new-window` do **not** format-expand their command, so
`--client '#{client_name}'` in a binding would pass the literal text. It is not
needed: inside a popup, tmux resolves the calling client from `$TMUX` correctly
(checked on tmux 3.4). A `--client` value that still contains `#{` is ignored
rather than treated as a client name.

### 9.2 Fallback for tmux < 3.2

```tmux
bind w new-window -n select tmux-session-selector
```

The temporary window closes itself when the program exits.

### 9.3 Command line

```
tmux-session-selector [flags] [initial query]

  --client NAME   tmux client to switch (default: the calling client)
  --regex         start in regex mode
  --no-preview    start with the preview hidden
  --refresh DUR   how often the preview updates (default 250ms; 0 for a
                  single snapshot)
  --command CMD   run CMD on the chosen target instead of switching to it;
                  see §8.1
  --sort ORDER    index (default), name or time; see §5
  --reverse       reverse the sort order
  --all-groups    show every session of a session group (tmux's -G)
  --version       print the version and exit
  --help          print usage and exit
```

A positional initial query pre-fills the prompt, so a binding such as
`bind W display-popup -E "tmux-session-selector s:work"` opens a pre-filtered
view.

### 9.4 Exit status

| Code | Meaning |
|---|---|
| 0 | switched to a target, or cancelled by the user |
| 1 | runtime error (tmux not found, no server running, tmux command failed) |
| 2 | invalid flags |

Errors go to stderr in one line. A popup or temporary window would close at
once and hide the message, so whenever the tool runs inside tmux (`$TMUX` set
or `--client` given) it waits for a key press before exiting on error. The
program cannot tell a popup from a shell, so a run from a shell inside tmux
waits too.

## 10. Robustness

- Names may contain any character, including spaces, `:`, `.`, `#`, quotes and
  non-ASCII text; rendering uses display width, not byte length.
- If a session, window or pane disappears between listing and `Enter`, the tmux
  error is shown on the prompt line and the list is reloaded; the program does
  not exit.
- The terminal is always restored (cooked mode, cursor visible, alternate
  screen left) on every exit path, including panics and `SIGTERM`.

## 11. Repository layout

```
cmd/tmux-session-selector/main.go   flags, wiring
internal/tmux/                      run tmux, parse list-panes output, build the tree
internal/filter/                    query parsing and matching (pure, no I/O)
internal/ui/                        terminal raw mode, input decoding, rendering
.github/workflows/ci.yml
.github/workflows/release.yml
README.md                           install, key binding, query syntax
specs.md                            this document
```

## 12. Testing

- **Unit tests** for `internal/filter`: words, smart case, negation, field
  prefixes, regex, invalid regex, ancestor/descendant visibility.
- **Unit tests** for `internal/tmux` parsing, using recorded `list-panes`
  output that includes awkward names.
- **Unit tests** for `internal/ui`: key decoding, tree visibility, selection
  rules, line truncation with wide characters.
- **Integration tests** that start an isolated server (`tmux -S
  <test temp dir>/sock -f /dev/null`), create sessions, windows and panes, and
  check the built tree. The switch test attaches a real client through a
  pseudo-terminal (`script`, Linux only) and checks where it lands. Tests
  always pass an explicit socket: a bare `tmux` command inside a tmux session
  talks to the user's own server through `$TMUX`.
- **Benchmark** for §7.5 (`BenchmarkKeystroke`, 500 panes).
- `gofmt`, `go vet` (Linux and macOS) and the tests run in CI.

## 13. Build and release

### 13.1 CI (`ci.yml`)

On every push and pull request: `gofmt` check, `go vet`, `go test ./...`
(with tmux installed for the integration test).

### 13.2 Release (`release.yml`)

On a pushed tag matching `v*`:

1. Run the CI checks (the release workflow calls `ci.yml`).
2. Build with `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X
   main.version=<tag>"` for:
   - `linux/amd64`, `linux/arm64`
   - `darwin/amd64`, `darwin/arm64`
3. Package each binary as `tmux-session-selector_<os>_<arch>.tar.gz`, with
   `README.md` and `LICENSE` when present, all at the archive root. The names
   carry no version, so the `releases/latest/download/…` URL stays stable.
4. Write `checksums.txt` (SHA-256).
5. Create a GitHub Release for the tag with the archives and checksums
   attached, and notes generated from the commits since the previous tag.

Only the Go toolchain and GitHub's own `gh` CLI are used; no third-party release
actions.

### 13.3 Install

```sh
curl -L https://github.com/mauricioprado00/tmux-session-selector/releases/latest/download/tmux-session-selector_linux_amd64.tar.gz \
  | tar -xz -C ~/.local/bin tmux-session-selector
```

or `go install github.com/mauricioprado00/tmux-session-selector/cmd/tmux-session-selector@latest`.

## 14. Later (not in v1)

- Kill the selected session, window or pane (with an in-UI `y/n` confirmation).
- Rename from the list.
- Sort by recent activity instead of by name.
- Remember the last query between runs.
