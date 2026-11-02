# tmux-session-selector

## Toggles

Every on/off toggle the user can switch (a key such as `Ctrl-V` preview,
`Ctrl-Z` background processes, `Ctrl-L` titles, `d` directories in the
overview) follows two rules:

- **It shows when it is on.** Its hint in the footer is drawn in `styleOn`
  instead of dim, and in the help (`?`) its key is in `styleOn` with `on`
  after it. Add the toggle to `togglesOn()` in `internal/ui/ui.go`, keyed by
  its footer hint exactly as spelt there ("^Z bg jobs") and by its help key
  ("Ctrl-Z"); footer hints are separated by two spaces.
- **It is remembered between runs** when it is a setting rather than a
  passing state: saved with `saveSetting` as `name=on|off` in the state file
  and read back in `Run`. Remembered now: `preview`, `background-jobs`,
  `claude-titles`, `overview-dirs`. Not remembered: regex mode and the
  overview's centre, which belong to the query and the view at hand.

Document both in README.md and specs.md along with the toggle.

## Building

`/usr/local/bin/go` on this machine is a Docker wrapper that fails without a
terminal; build and test with `make build GO=/usr/bin/go` and
`/usr/bin/go test ./...`.
