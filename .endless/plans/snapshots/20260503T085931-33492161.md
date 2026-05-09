# E-1130 — Skip endless-hook work when cwd is in a worktree but binary is the global one

## Context

E-998 ships a per-worktree `.claude/settings.json` so a Claude session inside a worktree fires `<worktree>/bin/endless-hook` instead of swapping the global `/usr/local/bin/endless-hook` symlink. E-1129 verified that Claude Code MERGES hook entries across user/project/local settings layers — does not replace. As a result, a session whose cwd is inside the worktree fires BOTH binaries for every event. State-mutating event handlers (`InitSession`, `ParseTranscript`, `SetProcess`, `RegisterTaskFile`, `EndSession`, …) execute twice for that session: duplicate DB rows, duplicate companion-file writes, duplicate task-context injection.

E-998's cross-session safety goal is intact (sessions outside the worktree still fire only the global), so this is a worktree-session-only side effect. The fix lives entirely in the global binary: when invoked from a cwd inside an endless-managed worktree that has its own `bin/endless-hook` (different file from the running binary), the global short-circuits before any state mutation. The worktree binary handles the event alone.

This is a developer-only concern — end users of endless never run two binaries on the same hook event.

## Approach

Add an early self-skip check in `cmd/endless-hook/claude.go` `runClaude`, immediately after `projectID` lookup (line 84) and BEFORE activity throttle/recording (line 87). All mutations — including `RecordActivity` — are guarded by the same skip.

### Skip logic (pseudocode)

```
if projectID == 0 || cwd == "" → no skip (run normally)
projectRoot ← monitor.ProjectPath(projectID)        // existing helper
worktreeRoot ← monitor.FindWorktreeRoot(cwd, projectRoot)  // existing helper
if worktreeRoot == "" → no skip (cwd is in main, foreign, or unrelated)

worktreeBin ← <worktreeRoot>/bin/endless-hook
worktreeStat ← os.Stat(worktreeBin)

if worktreeStat == nil and err is ErrNotExist:
    LOUD WARN: "cwd <cwd> is in worktree <worktreeRoot> but <worktreeBin> does not exist — running global as fallback. Run 'just build' in the worktree to enable the override."
    no skip (run normally)

if worktreeStat == nil and err is other:
    log error, no skip (run normally — never break the hook)

selfPath ← os.Executable()
selfStat ← os.Stat(selfPath)
if os.SameFile(selfStat, worktreeStat):
    no skip (I AM the worktree binary)

# We are the global, the worktree binary exists, we are inside the worktree:
log "deferring to <worktreeBin> (cwd is in worktree)"
return nil  // skip all event handling
```

### Why these choices

- **Identity check via `os.SameFile`.** Wraps the `(dev, ino)` comparison the user picked. Robust against the `/usr/local/bin/endless-hook` → `<main>/bin/endless-hook` symlink and any future symlink layout — `os.Stat` follows symlinks by default. No string/path matching.
- **Asymmetric design (only the global skips).** The worktree binary doesn't need to check anything — the global yielding is sufficient. This avoids a chicken-and-egg "should I run if the other is going to?" coordination.
- **Loud warn on missing worktree binary.** Per the configuration-must-loudly-complain principle: the hook still runs the global so the Claude session keeps working, but writes a clearly-marked message to both stderr and `<ConfigDir>/log/hook.log` (existing logger at `claude.go:23-40`). The user cannot miss a missed `just build`.
- **Skip BEFORE activity throttle.** The throttle would naturally dedup most concurrent fires, but not all (race within the throttle window) and the throttle has nothing to do with the architectural reason both fire. Skipping earlier is cleaner.
- **`os.Executable()` failure → no skip.** Defensive: if we can't determine our own path we run normally rather than break the hook.

## Critical files

- **`/Users/mikeschinkel/Projects/endless/cmd/endless-hook/claude.go`** — add skip helper, call it from `runClaude` between current line 84 and line 86. Also add an import for any new stdlib (none new — `os`, `filepath`, `log` already imported).
- **`/Users/mikeschinkel/Projects/endless/internal/monitor/worktree_lock.go`** — read-only: reuse `FindWorktreeRoot` (lines 220-245) verbatim.
- **`/Users/mikeschinkel/Projects/endless/internal/monitor/`** — read-only: reuse `ProjectPath` (already used by `handleWorktreeAdoption` at `claude.go:988`).

No new files. No package boundary changes. Skip helper lives as a private function in `claude.go` next to `handleWorktreeAdoption` (logically related).

## Tests

Two layers:

**Unit (Go).** New `cmd/endless-hook/claude_skip_test.go`. Cases:
- cwd outside any project → no skip
- cwd in main checkout (no worktree companion above it) → no skip
- cwd in worktree, worktree binary missing → no skip + warn log captured (assert via redirected log output)
- cwd in worktree, worktree binary exists, self == worktree binary → no skip
- cwd in worktree, worktree binary exists, self != worktree binary → SKIP

For the file-identity cases, the test creates a temp directory with `<wt>/.endless/worktree.json` (companion sentinel) and a `<wt>/bin/endless-hook` file. The "self" is faked by passing in the path used as `os.Executable` via a small seam (an injected function or a package-level var the test overrides). The exploration's logger writes to stderr+file via `log.SetOutput`; the test redirects `log.SetOutput` to a `bytes.Buffer` to assert the warn message.

**End-to-end (manual, recorded in commit).** From inside `.endless/worktrees/e-NNN/`:
1. `just build` → ensures `bin/endless-hook` exists.
2. `just claude-settings-init` → installs `.claude/settings.json`.
3. `endless task spawn <task>` → opens a tmux pane with Claude.
4. In that Claude session, type any prompt (fires UserPromptSubmit).
5. Tail `<ConfigDir>/log/hook.log` and grep for the new "deferring to" log line — should see exactly one (the global's deferral). The worktree binary's normal hook handling lines should also appear.
6. Query the events DB for rows with that session_id at the prompt timestamp — should be exactly one set, not two.
7. Spawn a second Claude session OUTSIDE any worktree (e.g., from `~/`). Confirm no "deferring to" line — global runs normally because cwd is not in a worktree.

## Out of scope

- Changing `claude-settings-init` to write `settings.local.json` instead — same merge rule applies, no benefit.
- Rolling back E-998 — sibling-session safety is the bigger win and stays.
- Detecting a "stale" worktree settings.json (settings file present but worktree gone) — the recipe is paired with worktree creation; mismatch only happens if someone hand-deletes `bin/` after running the recipe. The loud-warn covers it.
- Any change to the worktree binary's behavior — asymmetric design is intentional.

## Verification checklist before marking verify

- [ ] Unit tests pass: `go test ./cmd/endless-hook/...`
- [ ] All other tests pass: `just test` (Python), `go test ./internal/monitor/...`
- [ ] Manual E2E above produces exactly one "deferring to" log line per worktree-session event and exactly one set of DB rows.
- [ ] Manual E2E from main (no worktree) shows NO "deferring to" log lines and the global runs normally.
- [ ] Loud-warn case verified by deleting `<worktree>/bin/endless-hook` and re-firing: stderr and hook.log both contain the warning, hook still runs, Claude session is not broken.
- [ ] E-1130 plan attached to the task via `endless task update E-1130 --text .endless/plans/E-1130.md` and committed in the same commit.
- [ ] Branch lands on main; worktree removed; `just install` from main; status → `verify`.
