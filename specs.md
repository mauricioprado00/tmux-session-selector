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
| Dependencies | Go standard library plus `golang.org/x/term` (raw mode, terminal size). No TUI framework; the screen is drawn with plain ANSI escape sequences. Any further dependency needs a stated reason. |
| tmux interaction | Only by running the `tmux` binary (`list-panes`, `capture-pane`, `switch-client`, …). No parsing of tmux internals or sockets. |
| Minimum tmux | 3.2 for popup mode (`display-popup`). Older versions work through the fallback launch described in §9. |
| Binary name | `tmux-session-selector` |

## 5. Data model

All data comes from a single call:

```
tmux list-panes -a -F '<format>'
```

with fields separated by a character that cannot appear in names (`\x1f`, the
ASCII unit separator). Fields read per pane:

| Level | Fields |
|---|---|
| Session | `session_id`, `session_name`, `session_attached`, `session_windows`, `session_activity` |
| Window | `window_id`, `window_index`, `window_name`, `window_active`, `window_panes`, `window_activity` |
| Pane | `pane_id`, `pane_index`, `pane_active`, `pane_current_command`, `pane_current_path`, `pane_title` |

Plus `client_session` of the invoking client, to mark and pre-select the current
location.

Rows are grouped into a tree: session → window → pane. Sessions keep tmux's
order (by name); windows are ordered by index, panes by index. Windows with a
single pane do not show a pane row; the window row stands for that pane.

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
 ──────────────────────────────────────────────────────────────────
 <preview of the selected pane>
```

- **Prompt line**: the query, the matching mode (`words` or `regex`), and the
  count of matching rows against the total.
- **Tree**: one row per session, window and pane.
  - Session row: name, window count, `attached` when a client is attached.
  - Window row: `index: name`; for single-pane windows also the command and
    path.
  - Pane row: pane id, current command, current path (with `$HOME` shown as
    `~`).
  - The client's current session, window and pane are marked (bold or `*`).
  - Rows shown only as context for a match (see §7.4) are dimmed; the matched
    text is highlighted.
- **Preview** (toggle, on by default when the terminal has at least 30 lines):
  the visible content of the selected pane, from
  `tmux capture-pane -p -t <pane_id>`. For a session or window row, the
  preview shows that row's active pane. Plain text in v1; colours may come
  later.
- Long rows are truncated with `…`; the screen never wraps.
- The UI redraws on terminal resize (`SIGWINCH`).

### 6.2 Initial state

- Query empty, all sessions expanded to window level, the current window
  selected.
- Mode `words`.

### 6.3 Key bindings

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
| `Enter` | jump to the selected target and exit |
| `Esc` / `Ctrl-C` / `Ctrl-G` | exit without changing anything |

Letters are always query input, never commands, so typing never triggers an
action by accident. Mouse support is out of scope for v1.

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
- If the selected row disappears, the selection moves to the first matching row.
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
| Window | `switch-client -t <session_id>` ; `select-window -t <window_id>` |
| Pane | as for window, then `select-pane -t <pane_id>` |

The commands are sent as one tmux invocation, separated by `\;`, so the switch
is atomic from the user's point of view. IDs (`$1`, `@3`, `%7`) are used rather
than names, so names containing `:`, `.` or spaces cannot be misparsed.

`switch-client` takes `-c <client>` when `--client` is given (see §9), so the
correct client switches even when several are attached.

When run **outside** tmux (`$TMUX` unset), the tool uses
`attach-session -t <target>` instead, replacing its own process with tmux
(`exec`).

## 9. Launching

### 9.1 Key binding (recommended)

In `~/.tmux.conf`, replacing the built-in chooser:

```tmux
bind w display-popup -E -w 80% -h 80% "tmux-session-selector --client '#{client_name}'"
```

`-E` closes the popup when the program exits.

### 9.2 Fallback for tmux < 3.2

```tmux
bind w new-window -n select "tmux-session-selector --client '#{client_name}'"
```

The temporary window closes itself when the program exits.

### 9.3 Command line

```
tmux-session-selector [flags] [initial query]

  --client NAME   tmux client to switch (default: the calling client)
  --regex         start in regex mode
  --no-preview    start with the preview hidden
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

Errors go to stderr in one line. In popup mode the popup would close at once
and hide the message, so on error the tool waits for a key press before exiting.

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
- **Integration test** that starts an isolated server (`tmux -L <random> -f
  /dev/null`), creates sessions, windows and panes, and checks the built tree
  and the result of a jump. It runs in CI on Linux, where tmux is installed in
  the job.
- `go vet` and `gofmt` checks in CI.

## 13. Build and release

### 13.1 CI (`ci.yml`)

On every push and pull request: `gofmt` check, `go vet`, `go test ./...`
(with tmux installed for the integration test).

### 13.2 Release (`release.yml`)

On a pushed tag matching `v*`:

1. Run the CI checks.
2. Build with `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X
   main.version=<tag>"` for:
   - `linux/amd64`, `linux/arm64`
   - `darwin/amd64`, `darwin/arm64`
3. Package each binary as
   `tmux-session-selector_<version>_<os>_<arch>.tar.gz` with `README.md` and the
   licence.
4. Write `checksums.txt` (SHA-256).
5. Create a GitHub Release for the tag with the archives and checksums
   attached, and notes generated from the commits since the previous tag.

Only the Go toolchain and GitHub's own `gh` CLI are used; no third-party release
actions.

### 13.3 Install

```sh
curl -L https://github.com/<owner>/tmux-session-selector/releases/latest/download/tmux-session-selector_<version>_linux_amd64.tar.gz \
  | tar -xz -C ~/.local/bin tmux-session-selector
```

or `go install <module>/cmd/tmux-session-selector@latest`.

## 14. Later (not in v1)

- Kill the selected session, window or pane (with an in-UI `y/n` confirmation).
- Rename from the list.
- Coloured preview (`capture-pane -e`).
- Sort by recent activity instead of by name.
- Mouse selection.
- Remember the last query between runs.
