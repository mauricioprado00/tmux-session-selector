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
| Pane | `pane_id`, `pane_index`, `pane_active`, `pane_current_command`, `pane_current_path`, `pane_title`, `pane_left`, `pane_top`, `pane_width`, `pane_height`, `cursor_x`, `cursor_y`, `pane_marked`, `pane_pid` |

The geometry and cursor fields are only used to draw the preview (§6.4).

The current location (`client_name`, `session_id`, `window_id`, `pane_id`) is
read with `display-message -p` for the calling client, or from `list-clients`
when `--client` names one. It is used to mark and pre-select the current row.

A window linked into several sessions appears under each of them.

When the selector runs in a pane of its own, rather than in a popup, that pane
is left out of everything it shows (list, preview, overview, counts), so it
never previews itself. The pane is found by matching the selector's terminal
(`tty`) against `pane_tty`; in a popup none matches. Its window goes too if
nothing else is in it, as with the temporary window of an `--open window`
binding, and its session if that is left empty. If that window was the
client's current one, the window tmux flags as last (`-`), the one the client
came from, stands in for it: it is marked current (`*`), selected at start,
the target of `Alt-h`, and its session's active window in previews and the
overview.

Sessions in a session group share their windows, so, like tmux's chooser,
only one session of each group is shown unless `--all-groups` (tmux's `-G`)
is given: the session the client is in, if it is in the group, else the
group's first (lowest id). Its row says `group NAME: a,b`. Fields read for
this: `session_group`, `session_group_list`.

Rows are grouped into a tree: session → window → pane. Windows with a single
pane do not show a pane row; the window row stands for that pane.

A pane running Claude Code shows its session title after its command. Claude
Code keeps the title in the pane's title (`pane_title`), starting with `✳`, or
a braille spinner character while it works, so it needs no extra call. A
title like that is shown unless a shell is in the pane's foreground, which
means Claude Code has exited and left its last title behind. The overview
(§6.9) writes the title on the pane's top border, after the window's label
for panes along the window's top. `Ctrl-L` hides or shows the titles in the
list and the overview alike, from either, and is remembered as
`claude-titles=on|off` in the state file (§6.4); they start shown.

#### Background processes

`Ctrl-Z` (normal mode: `z`) shows, under each pane's row, that pane's
background processes: jobs stopped with `Ctrl-Z`, or sent to the background
with `bg` or `&`. They are read with a single
`ps -A -ww -o pid=,ppid=,pgid=,tpgid=,stat=,args=` when shown, and again when
the tree is reloaded, never while hidden. A process is one when it descends
from the pane's first process (`pane_pid`, its shell), has a controlling
terminal (`tpgid` > 0), and is neither in the terminal's foreground process
group nor in the shell's own group. Each is put under its nearest background
ancestor, so a job shows as the tree of its processes. Rows read
`PID command  stopped` (`stat` starting with `T`) or `… running`; the rows
above them are expanded so they show, and the prompt line counts them
(`3 background`). The search matches them by command (`c:`), with session and
window names as for panes.

`Enter` or a click on one jumps to its pane, and the preview shows the pane.
They are not tmux targets: kill, tag and command on them do nothing but say
so, and `Ctrl-T` does not tag them, so none of these can hit the whole pane
by mistake.

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
help panel, in colour (headings bold cyan, keys yellow, search examples green,
the mode names in the prompt's `NORMAL` style): the query syntax (words, `!`, `s:`, `w:`, `c:`, `d:`, `/regex/`,
smart case), every key binding and the mouse actions. When it does not fit,
`Up`/`Down`, `PgUp`/`PgDn`, `Home`/`End` and the wheel scroll it, and its last
row says so. Any other key closes it; keys other than `Esc`, `Enter` and `F1`
also take effect, so typing goes straight into the query.
`F1` alone is not enough because some terminals (GNOME Terminal) keep it for
their own help. The same query syntax is printed by `--help`.

### 6.3 Initial state

- Query empty, all sessions expanded to window level, the current window
  selected.
- Mode `words`.
- Preview shown as it was left last time (§6.4), or, if it has never been
  toggled, when the terminal is at least 30 rows high. `--no-preview` starts
  it hidden without changing what is remembered. Background processes hidden.

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
- **Remembered**: `Ctrl-V` saves whether the preview is shown, as
  `preview=on` or `preview=off` in `$XDG_STATE_HOME/tmux-session-selector/state`
  (`~/.local/state/…` when `XDG_STATE_HOME` is unset), and the next run starts
  that way (§6.3). The file holds `name=value` lines and is replaced whole,
  through a temporary file and a rename. If it cannot be written, the prompt
  line says so and the toggle still works for this run.

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
| `Ctrl-V` | toggle the preview, and remember it for the next run |
| `Ctrl-Z` | show or hide the panes' background processes (§5) |
| `Ctrl-L` | show or hide Claude Code session titles, here and in the overview (§5) |
| `Ctrl-/` | switch between search and normal mode (§6.8) |
| `Ctrl-O` | open the overview of the selected row's session (§6.9) |
| `Ctrl-B` / `Ctrl-F`, `Ctrl-D` | page up / down, half a page down |
| `?` (empty query) / `F1` | open the help panel |
| `<` / `>` (empty query) | scroll the windows of a session preview |
| `Enter` | jump to the selected target and exit |
| `Esc` / `Ctrl-C` / `Ctrl-G` | exit without changing anything |

In search mode letters are always query input, never commands, so typing
never triggers an action by accident. `?` opens help only when the query is
empty; anywhere else it is query text; the same holds for `<` and `>`. In
normal mode (§6.8) letters are commands.

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
Kill Tagged, Cancel, plus Overview (§6.9, letter `o`). Each shows the key that does the same without the menu.
It opens at the pointer, moved to fit on screen, and is drawn over the list
and preview. While it is open: a click on an item, `Up`/`Down` and `Enter`,
or tmux's letter for the item (`t`, `T`, `a`, `m`, `x`, `X`, `q`, `l`/`h`)
picks it; any other key or click closes it without acting or typing. In a popup or pane,
tmux forwards mouse events only when its `mouse` option is on (checked with
tmux 3.4, in both a window and a `display-popup`).

### 6.7 Commands

The commands of tmux's chooser (`choose-tree`) are available in both modes
(§6.8): in search mode on the same letters pressed with **Alt**, since plain
letters and digits are query text; in normal mode on tmux's plain keys. The
table gives the search-mode key. A terminal sends Alt-x as `ESC x`; `ESC O`
not followed by a known SS3 key is Alt-O.

| Key | tmux | Action |
|---|---|---|
| `Alt-x` | `x` | Kill the selected session, window or pane, after a `y/n` question on the prompt line (`kill-session`, `kill-window` or `kill-pane`). The tree is reloaded and the selection stays at the same position. |
| `Alt-t` | `t` | Tag or untag the selected row, then move down. Tagged rows are shown in bold magenta with a `+` in the gutter, and the prompt line counts them. Tags are kept by target, so they survive reloads. |
| `Alt-T` | `T` | Untag everything. |
| `Ctrl-T` | `C-t` | Tag every row that matches the query (context rows are not tagged). |
| `Alt-m` | `m` | Make the selected pane (the active pane of a selected window or session) tmux's marked pane (`select-pane -m`), the default source for `join-pane`, `swap-pane` and `move-pane`. The tree reloads so the `M` flags update. |
| `Alt-M` | `M` | Clear the marked pane (`select-pane -M`). |
| `Alt-X` | `X` | Kill every tagged item, after a `y/n` question; panes go first, then windows, then sessions, so none has gone before its turn. With nothing tagged, the selected row. |
| `Alt-:` (or `Alt-;`) | `:` | Read a tmux command on the prompt line and run it on every tagged item (or the selected row if none), with `%%` and `%1` replaced by each target as in §8.1. `Esc` cancels. The tree is reloaded afterwards. |
| `Alt-o` (or `Alt-O`) | `O` | Change the sort order: index → name → time (§5). |
| `Alt-r` | `r` | Reverse the sort order. |
| `Alt-h` (or `Alt-H`) | `H` | Select the pane the selector was started from, expanding its window, and clearing the query if it hides that pane. Does nothing outside tmux. |
| `Alt-0` … `Alt-9` | `0` … `9` | Jump straight to the row with that label. Labels are a dim gutter on the left of the rows on screen, numbered from the top of the tree area (tmux numbers from the top of the whole list), so after filtering the first label is the first row shown. Search mode labels ten rows `M-0` … `M-9`; normal mode labels 36, `0` … `9` then `M-a` … `M-z`, as tmux does. |
| `Alt-+` (or `Alt-=`) / `Alt--` | `M-+` / `M--` | Expand / collapse every session and window. The selection moves to the nearest visible ancestor if it is hidden. |

While a question is shown, `y` or `Y` confirms and any other key cancels.

### 6.8 Modes

There are two modes, switched with `Ctrl-/` (terminals send it as `0x1f`, the
same byte as `Ctrl-_`).

- **Search mode** (the default): typing edits the query, which filters the
  list as described in §7; tmux's commands are on Alt (§6.7).
- **Normal mode**, named after vim's: keys are commands, as in tmux's own
  chooser, with vim's motions. The query keeps filtering but cannot be
  edited; the prompt line shows `NORMAL` and no cursor, and the footer lists
  normal-mode keys. `--normal` starts in it.

| Normal-mode key | Action |
|---|---|
| `j` / `k` | down / up |
| `h` / `l` (or `-` / `+`) | collapse / expand |
| `g` / `G` | first / last row |
| `Ctrl-U` / `Ctrl-D` | half a page up / down |
| `0` … `9`, `M-a` … `M-z` | jump to the row with that label |
| `t` / `T` / `Ctrl-T` | tag / untag all / tag every match |
| `x` / `X` / `:` | kill / kill tagged / command for tagged |
| `m` / `M` | mark / unmark the pane |
| `O` (or `o`) / `r` | change sort order / reverse |
| `H` | back to the starting pane |
| `n` / `N` | next / previous row matching the query, wrapping around |
| `v` | show or hide the preview |
| `z` | show or hide background processes |
| `<` / `>` | scroll a session preview |
| `?` | help |
| `/` or `f` | back to search mode (tmux's `f` is its filter) |
| `q` | quit |

Keys that are not letters work the same in both modes (arrows, `Enter`,
`Esc`, `Ctrl-T`, `Ctrl-R`, `Ctrl-V`, `Ctrl-B` / `Ctrl-F` page, `Ctrl-D` half
page, Alt-+ / Alt--), except that in normal mode `Ctrl-U` is half a page up
instead of clearing the query, and `Backspace` / `Ctrl-W` do nothing. The
right-click menu shows the key of the current mode for each item.

### 6.9 Overview

`--overview` opens the selector straight into the overview of the client's
current session, its windows sharing the screen evenly with the focus on the
current window; there is no list behind it, so `Esc` and `q` close the
selector. Otherwise, `Ctrl-O` (both modes, or Overview in the right-click
menu) replaces the list with every window of the selected row's session at once, for watching
them. Each window is drawn as in the preview (§6.4): its panes in their
layout, in colour, and live, captured again every `--refresh`.

- From a **session row**, the windows share the screen evenly in rows; the
  number of columns is the one that shows the windows largest, and a shorter
  last row is spread across the full width. The session's active window has
  the focus.
- From a **window or pane row**, that window takes the middle half of the
  screen each way (50% × 50%) and has the focus; the other windows go around
  it, clockwise from the top left. The grid is 4 × 4 cells (12 around the
  centre), or 8 × 8 (48) and so on when there are more windows; the layer
  next to the centre fills first.

The top line names the session and the layout; the bottom line lists the
keys. The window in focus has a bold yellow frame and a reversed `▶` label.

| Key | Action |
|---|---|
| arrows, `h` `j` `k` `l` | move the focus to the next window that way. A *focus point* inside the focused window remembers where the focus came in: moving up or down keeps its column, left or right its row, so passing through the large centre window comes out in line with where it went in (up twice from the window under the centre's right half reaches the one above that half). Of the windows beyond the focused one, those in line with the point come first, then those beside the focused window, then the closest, then the best aligned. With nothing that way, the focus wraps around to the farthest window the other way. Moving the focus any other way puts the point in the middle of the new window. |
| `Tab` / `Shift-Tab` | next / previous window by number, wrapping around |
| `0` … `9` | focus the window with that number; digits typed within a second of each other combine (`1` `7` is window 17), and a digit that makes no window number starts a new one |
| `Home` / `End` | first / last window |
| `Enter`, double-click | switch to the window in focus, as choosing it in the list does (`--command` applies) |
| click / wheel | focus a window / the next or previous one |
| `o`, `Ctrl-O` | put the window in focus in the centre; on the centred window, go back to the even grid |
| `+` / `-` | grow / shrink the window in focus by a step of 1.25×, up to six steps either way (about 3.8× or a quarter); the header shows its size when it is not 1 |
| `=` | give the window in focus size 1 again; on a window already at 1, give every window of the session size 1, both sizes, as vim's `Ctrl-W =` makes all windows equal (so `=` `=` resets everything) |
| `Ctrl-L` | show or hide Claude Code session titles, as in the list |

Sizes are relative: a window grows by taking room from the others. Each
window has two, both 1 at first: a *normal* size, for its place in the even
grid or the ring, and a *middle* size, for when it is the centre window. The
keys change the one in use, so a window grown in the centre goes back to its
normal size, however grown that is, when it leaves the centre.

Both layouts are grids of cells: the even grid has its rows and columns
(chosen for the number of windows, as before), and the centred view a k×k
grid whose middle half is the centre window and whose ring of cells around
it holds the others. Windows fill the cells in order: row by row in the even
grid, and in the ring the layer next to the centre first, each layer
clockwise from its top left corner.

- **Snapping.** A window takes more cells as it grows: two at about 2× (three
  steps), a 2×2 block at about 3.8× (six steps). Each time the windows are
  placed, every window goes at the first free cell in order, so the windows
  after a grown one move along, and empty cells of the grid are used up. Of
  two cells, the second goes beside or below, whichever makes the window's
  box closest to the shape of the real window (beside when equal); a block
  that has no room where the window goes (a ring one cell deep) is two cells
  instead.
- **Only spare cells.** A grid has as many spare cells as it has cells beyond
  one per window. They go to the largest windows first; a window that cannot
  have the cells it asks for keeps fewer.
- **Like % in an HTML table.** What a window's cells do not cover of its size
  (all of it when it has not snapped, or is shrunk) stretches the columns and
  rows it is in: each column or row is as wide or tall, relative to the
  others, as the mean of the windows in it. So a window in a full grid grows
  by widening its column and row, and the windows sharing them grow with it.
- In the even grid, a row left with empty cells whose windows all lie within
  it spreads them over the full width, as the last row always did.
- In the centred view, the centre's middle size scales its columns and rows
  (size 1 is the middle half); the centre does not snap.

The layout stays a grid of rows and columns, which a live window (`w`) is
built from, sizes and all; in the rare arrangement of spanning windows that
cannot be cut into rows and columns, the live window keeps a tiled layout and
says so.

Sizes are remembered between runs in the state file (§6.4), by window id, as
`size.@3=N,M`: the normal and middle sizes in steps. Saving drops the sizes
of windows that have closed, as tmux may give their ids to new windows.
| `Esc`, `q` | back to the list |

On every refresh tick the session is read again, so windows opened or
closed meanwhile appear or go and the layout adjusts; the focus and the
centre stay on the same windows by id. If the centre window closes, the
windows share the screen evenly; if the whole session goes, the view closes
with a note. Returning to the list reads it again if windows changed.

All panes are captured in one tmux call; if that fails, because a pane closed
since it was listed, they are captured one by one, so the others keep their
content (this applies to the preview too).

If switching fails, the overview closes and the error shows on the list.

#### Live window

`w` in the overview (after a `y/n` question) turns what it shows into a real,
interactive tmux window: every window of the session becomes panes of the
window in focus, laid out as the overview draws them.

1. The overview's boxes are cut into nested rows and columns (rows first),
   which is how tmux layouts are made; each part weighs as much as it spans,
   so empty ring cells are shared out. Each window's own panes keep their
   arrangement inside its part (its `window_layout`, resized in proportion).
2. What each window was is saved in the window option
   `@tmux-session-selector-overview` (JSON: name, index, layout, panes, active
   pane, its own `automatic-rename`), before anything changes.
3. Zoomed windows are unzoomed; every other pane is joined into the window in
   focus (`join-pane -d`), re-tiling after each so tmux keeps room; then the
   computed layout is applied with `select-layout` (checksummed as tmux does).
   If that fails, the tiled layout stays and the prompt line says why.
4. The window is renamed `overview` and the client switches to it.

When the selector next starts from a live window (it is the client's current
window, as §5 decides it), it first splits it back: each saved window is
broken out again (`break-pane -d`) at its old number if free, its other panes
joined back, and its layout, active pane, name and `automatic-rename`
restored; the window in focus gets its own back. Panes closed meanwhile are
skipped; panes added meanwhile stay in it. Then the list opens as usual, with
a note on the prompt line.

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

### 9.3 Binding from the binary

`--tmux-bind` and `--tmux-unbind` do 9.1 or 9.2 for the user, then exit.

- `--tmux-bind [--action both|select|overview] [--key KEY] [--overview-key KEY]
  [--open full|popup|window] [--config PATH] [-- ARGS…]` binds, in tmux's
  prefix table, the selector (`select`: the list, on `--key`, default `w`,
  replacing tmux's chooser) and the overview (`overview`: the selector run
  with `--overview`, on `--overview-key`, default `O`), or just the one
  `--action` names; both by default. The two keys must differ. The selector is
  given by the binary's absolute path (`os.Executable`), so tmux's `PATH` does
  not matter; `ARGS` are added to each bound command.
  `--open` says where it opens:
  - `full` (the default): `display-popup -E -B -w 100% -h 100%`, a borderless
    popup covering the whole terminal, status line included. It looks like a
    window of its own, but none is created, so nothing appears in the window
    list and the selector never previews itself.
  - `popup`: `display-popup -E -w 80% -h 80%`, over the current window.
  - `window`: `new-window -n select`, a real window, closed when the selector
    exits; for tmux before 3.2. The selector leaves this window out of what it
    shows (§5).

  Popups came in tmux 3.2 and borderless ones in 3.3 (from `tmux -V`; a
  version that is not a release number counts as recent). On older tmux,
  `popup` and `full` fall back to `window`, and on 3.2 `full` keeps its
  border; the output says so. Words are quoted for the shell and then for tmux's
  configuration syntax, which expands `$VARIABLES` inside double quotes.
- The configuration file is `--config`, or else the one tmux reads: the first
  that exists of `~/.tmux.conf`, `$XDG_CONFIG_HOME/tmux/tmux.conf`,
  `~/.config/tmux/tmux.conf`, else `~/.tmux.conf`, created if missing. The
  select binding goes in a block between `# >>> tmux-session-selector >>>` and
  `# <<< tmux-session-selector <<<` (the overview's between
  `# >>> tmux-session-selector overview >>>` and its `<<<` line), after one
  blank line, which also records
  the key and what it was bound to before (`# previous:`, as `list-keys`
  prints it). Binding again replaces the block. The file is written in place,
  keeping its mode and following a symlink, as dotfiles often are.
- If a tmux server is running, the binding is applied to it at once with
  `bind-key`; moving the binding to another key restores the old key's
  previous binding.
- `--tmux-unbind [--action both|select|overview] [--config PATH]` removes each
  binding's block (both by default), and the blank line before
  it, and in a running server unbinds the key and re-runs the recorded
  previous binding, e.g. `bind-key -T prefix w choose-tree -Zw`.

Each prints what it did. Exit status 0, 1 on an I/O or tmux error, 2 for an
invalid `--key` or `--open`.

### 9.4 Command line

```
tmux-session-selector [flags] [initial query]

  --client NAME   tmux client to switch (default: the calling client)
  --regex         start in regex mode
  --normal        start in normal mode (§6.8)
  --no-preview    start with the preview hidden, whatever it was left at
  --refresh DUR   how often the preview updates (default 250ms; 0 for a
                  single snapshot)
  --command CMD   run CMD on the chosen target instead of switching to it;
                  see §8.1
  --sort ORDER    index (default), name or time; see §5
  --reverse       reverse the sort order
  --all-groups    show every session of a session group (tmux's -G)
  --overview      open straight into the overview of the current session
                  (§6.9); Esc or q then closes the selector
  --tmux-bind     bind the selector and the overview in tmux and exit; §9.3
  --tmux-unbind   remove those bindings and exit
  --action WHICH  for --tmux-bind / --tmux-unbind: both (default), select or
                  overview
  --key KEY       the selector's key for --tmux-bind (default w)
  --overview-key KEY  the overview's key for --tmux-bind (default O)
  --open MODE     for --tmux-bind: full (default), popup or window
  --config PATH   the tmux configuration file for --tmux-bind / --tmux-unbind
  --zoom          zoom the pane the selector runs in while it runs (tmux's
                  -Z); see below
  --version       print the version and exit
  --help          print usage and exit
```

`--zoom` finds the selector's own pane by matching its terminal (`tty`)
against `pane_tty`, zooms the window on it if it is not zoomed already, and
unzooms it on exit, but only if it is still zoomed by us. In a popup no pane
has the selector's terminal (`$TMUX_PANE` there names the pane underneath), so
nothing is zoomed.

A positional initial query pre-fills the prompt, so a binding such as
`bind W display-popup -E "tmux-session-selector s:work"` opens a pre-filtered
view.

### 9.5 Exit status

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
internal/tmuxconf/                  --tmux-bind / --tmux-unbind: the config block and live binding
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
