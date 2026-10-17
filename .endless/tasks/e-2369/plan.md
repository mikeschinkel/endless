# Plan: refuse $HOME as a project

## Approach

`monitor.ResolveDirectory` (internal/monitor/project_ignore.go) is the one place that answers "which project is this directory in". The hook's auto-registration and Python's register, discover and reconcile (through `endless-go project resolve`) all use it. Teach it the rule once, and every path follows.

Rule: $HOME and every ancestor of it (`/Users`, `/`) are never a project. $HOME comes from `os.UserHomeDir`, resolved the way `resolvedHomeDir` already does. This works the same on any machine and for any user. It is not tied to this machine's layout.

1. **ResolveDirectory**
   - For `dir` == $HOME or an ancestor of it, return a new verdict, `VerdictHome`. Callers treat it like ignored: nothing to record, never auto-register.
   - For `dir` below $HOME, the walk-up stops before $HOME. A row at $HOME is never matched, so a repo under ~/Projects with no config of its own resolves to None. It no longer inherits the home row.
   - `deepestRowForResolvedPath` skips any row whose resolved path is $HOME or an ancestor.
   - Result: the existing home row stops resolving as soon as this lands, with no data change.
2. **ProjectIDForPath and the hook.** `VerdictHome` maps to `ErrIgnoredDirectory`, or a sibling error that the hook treats the same way. A Claude session started in ~ records nothing and registers nothing.
3. **Python register, init and discover.** An explicit `project register ~` (and `init`, and discover offering ~) is refused with a clear message: "$HOME cannot be an Endless project; register a directory under it". `project ignore ~` is unnecessary but harmless.
4. **Python `project_name_for_cwd`.** Skip the on-disk `<cwd>/.endless/config.json` check when cwd is $HOME, so a stray `~/.endless/config.json` cannot make home a project.
5. **cwd-defaulted verbs** (`project status`, `project monitor`, `task add`, and so on). Run from ~, they now fail with "not in a registered project, name one". Check that the message says so, rather than a generic error. No new NAME requirement: defaulting to the enclosing project is intended, and `--tmux` already requires NAME.
6. **The existing home row** (`mikeschinkel`, path `~`). After step 1 it is inert. `project list` should flag it or omit it. The user removes it with `endless project unregister`, which is a user command, not an agent one. Do not hand-edit the database.
7. **Stray ~/.endless/db-ledger.** It holds two events (E-1063 phase, E-2336 claim) stamped with project "mikeschinkel", written from a cwd under ~. Leave the file untouched: ledgers are never hand-edited. Stamping events by cwd is E-2216's subject. Add a note on E-2216 that this is evidence, and ask the user whether those two events need replaying into endless's ledger.

## Tests

- Go: `ResolveDirectory` table tests with a temp $HOME. Cover:
  - $HOME → home verdict
  - an ancestor of $HOME → home verdict
  - $HOME/Projects/x with a row at $HOME → none
  - $HOME/Projects/x with its own row → project
  - an ignored row and a marker still win below $HOME
  - a legacy-spelling scan skips the home row
- Go: hook payload with cwd = $HOME → no project row is created.
- Python: `project register` on $HOME refuses with the message; discover never offers $HOME.
- Manual: from ~, `endless project monitor` refuses with the "name a project" message, and `endless project monitor endless` works.

## Verify

`just test` and `just test-go` pass. From a fresh shell in ~, `endless project status` refuses cleanly. `endless project list` shows the home row flagged or hidden.
