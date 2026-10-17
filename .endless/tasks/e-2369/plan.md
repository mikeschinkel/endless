# Plan: refuse $HOME and other non-project directories as projects

## Rules

1. **Never a project, not editable.** $HOME itself, and the per-user roots above it (/Users on macOS, /home on Linux, C:\Users on Windows, and `/`). Directories outside $HOME stay allowed (/work/my-project in Docker, VMs, CI/CD).
2. **Disallowed, children allowed.** Well-known per-OS directories:
   - macOS: ~/Documents, ~/Desktop, ~/Downloads, ~/Library, ~/Movies, ~/Music, ~/Pictures, ~/Public
   - Linux: ~/Documents, ~/Desktop, ~/Downloads, ~/Music, ~/Pictures, ~/Videos, ~/Public, ~/.config, ~/.local, ~/.cache
   - Windows: ~/Documents, ~/Desktop, ~/Downloads, ~/AppData, ~/Music, ~/Pictures, ~/Videos
   The directory itself can never be a project. ~/Documents/foo can.
3. **Parent directory.** Not a project itself, AND the walk-up stops there. A directory below it never inherits a project registered above it. ~/Projects ships as a default parent directory. A directory becomes a parent directory in either of two ways:
   - an entry in the JSON overrides with `"parent_dir": true`
   - an on-disk `.endless-parent-dir` marker file in the directory, which travels with it and needs no config edit. At the same rung, the marker wins, as `.endless-ignore` does.

## Where the list lives

- **Built-in defaults** live in the Go binary, per OS.
- **User overrides** live in `~/.config/endless/disallowed-paths.json` (the machine config dir, `monitor.ConfigDir`). The file holds only additions, and removals of built-in entries. It is loaded on demand and created on first need with an empty, commented skeleton. New defaults in later versions reach every user automatically, and the user's edits still win. Rule 1 cannot be removed by an override.
- **Documented full list.** It is generated from the built-in defaults into `docs/guide/disallowed-paths.md`, readable with `endless guide disallowed-paths`. A `just` recipe regenerates it. A check fails `just test` when the doc drifts from the code, following the lifecycle-check pattern.

## Enforcement point

`monitor.ResolveDirectory` (internal/monitor/project_ignore.go) is the one resolver used by hook auto-registration and by Python's register, discover and reconcile (through `endless-go project resolve`).

- For a rule-1 or rule-2 directory, a new verdict, `VerdictDisallowed`, carries which rule matched. Callers treat it like ignored: never auto-register, record nothing.
- The walk-up never matches a row at a rule-1 or rule-2 rung, and stops at a parent directory. So the existing home row goes inert at once, and a repo under ~/Projects with no config resolves to none.
- `deepestRowForResolvedPath` applies the same skips and the same stop.
- Python `register`, `init` and `discover` refuse a disallowed directory, naming the rule and the overrides file. Python `project_name_for_cwd` skips the on-disk `.endless/config.json` check at a disallowed directory. No new SQLite in Python: the list is loaded in Go and reached through `endless-go project resolve`.

## The existing home row

Inert after this lands. `project list` marks a row whose path is disallowed. The user removes it (`endless project unregister mikeschinkel`).

## Out of scope (separate tasks)

- Refusing nested projects.
- A release valve for nested projects (git submodules), as a maybe.

## Tests

- Go table tests with a temp $HOME and temp config dir:
  - rule 1: $HOME, an ancestor of $HOME → disallowed
  - a directory outside $HOME → allowed
  - rule 2: ~/Documents → disallowed; ~/Documents/foo → allowed
  - a JSON override adds a directory; a JSON override removes a built-in entry; an override that tries to remove $HOME is ignored
  - a parent directory via JSON and via marker: walk stops, so a row above it is not inherited
  - an ignored row and `.endless-ignore` still win below
  - the legacy-spelling scan applies the same rules
- Go: hook with cwd = $HOME → no project created.
- Python: `project register` on $HOME and on ~/Documents refuses with the message; discover never offers them.
- Doc drift check: the generated guide page matches the built-in list.

## Verify

`just test` and `just test-go` pass. From ~, `endless project status` refuses cleanly. `endless guide disallowed-paths` prints the list.
