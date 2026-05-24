# E-1450 — Hook-fired DB writes always target the real DB

> **Landing note (2026-05-24, post-approval).** Between approval and landing, main diverged: `1fefb59`
> (E-1449, "Delete the plan-snapshot feature") removed `monitor.IsSandboxActive()` and `sandbox_test.go`
> as dead code. The implementation therefore **reintroduces `IsSandboxActive()`** (now E-1450's sole
> caller, as E-1362's ledger entry anticipated) and recreates `sandbox_test.go`. Consequently the
> *snapshot-commit gate* rationale referenced below (`claude.go:958`) is **obsolete** — that gate no
> longer exists. The DB-path-override approach still beats env-mutation for the remaining reason:
> env-mutation would also drag `hook.log` and global `config.json` reads out of the sandbox, whereas
> the chosen scope is DB writes only. A redundant auto-record ledger commit on the branch was skipped
> during rebase (its 16 entries were already present in main).

## Context

After E-1426 retired companion JSON files, the `sessions` table is ground truth for Claude-pane
registration. Inside a sandboxed self-dev worktree (`worktree_sandbox=true`), the worktree's
`.claude/settings.json` injects `XDG_CONFIG_HOME=<sandbox>`, so `monitor.ConfigDir()` — and therefore
`monitor.DB()` — resolves to the per-worktree sandbox DB. Every hook event (`SessionStart`,
`PreToolUse`, `PostToolUse`, `Stop`, `SessionEnd`) writes session registration / activity / state
transitions to the **sandbox** DB instead of the real one at `~/.config/endless/endless.db`.

Consequence: the real DB has no row for the live Claude pane, real-world activity lands in throwaway
test fixtures, and the E-1401 attribution gate refuses real-DB ledger writes ("no session row matches
this pane"). Reproduced live while writing this plan: `endless task detail E-1450` from this worktree
returns "No task found" (sandbox DB), while `XDG_CONFIG_HOME=$HOME/.config endless task detail E-1450`
finds it. During E-1396 the workaround was a manual `INSERT` into the real `sessions` table.

Intent (task description): **all hook-fired DB writes reflect real-world activity and belong in the
real DB**, regardless of sandbox routing.

## Decision: redirect the DB only, at the path layer

Confirmed with Mike: redirect **DB writes only**. The hook's `hook.log` and its reads of the global
`~/.config/endless/config.json` stay sandbox-routed (unchanged from today).

We do **not** reset `XDG_CONFIG_HOME` (the blunt env-mutation approach). That would drag the log and
config reads along with the DB, and — more dangerously — flip `monitor.IsSandboxActive()` to `false`
for the whole process, defeating the snapshot-commit gate at `cmd/endless-hook/claude.go:958` and
making a dev-worktree hook attempt snapshot commits onto the project branch.

We also do **not** add per-write-site `*Real` variants to the session helpers — unnecessary. The DB
path has a single chokepoint: `monitor.DBPath()` (`internal/monitor/db.go:55`) is the only place that
joins `ConfigDir()` + `endless.db` (verified by grep), and both the `DB()` singleton (`db.go:62`) and
`BackupDB()` (`db.go:124`) derive from it. Overriding `DBPath()` redirects every DB consumer in one
move while leaving `ConfigDir()` (log, config.json) untouched.

## Implementation

### 1. `internal/monitor/db.go` — add a DB-path override

Add a package var and a one-shot setter; have `DBPath()` honor it:

```go
var dbPathOverride string

// DBPath returns the path to the Endless SQLite database.
func DBPath() string {
	if dbPathOverride != "" {
		return dbPathOverride
	}
	return filepath.Join(ConfigDir(), "endless.db")
}

// ForceRealDB routes monitor.DB() and DBPath()-derived artifacts (backups) to
// the real database under ~/.config/endless, ignoring the sandbox XDG_CONFIG_HOME
// routing. Log files and global config.json reads are NOT affected — they keep
// following ConfigDir(), and IsSandboxActive() still reports true so sandbox-aware
// gates (e.g. the snapshot-commit skip) behave correctly. No-op when not
// sandbox-routed. Must be called before the first DB()/DBPath() use. See E-1450.
func ForceRealDB() {
	if !IsSandboxActive() {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dbPathOverride = filepath.Join(home, ".config", "endless", "endless.db")
}
```

`IsSandboxActive()` reads `ConfigDir()`/`CacheDir()` (env), so it must be evaluated *before* we set
the override — which it is. We never mutate env, so `IsSandboxActive()` keeps returning the truth for
the rest of the process.

### 2. `internal/monitor/db.go` — backups follow the DB

`BackupDB()` reads `src := DBPath()` (will be real) but writes to `filepath.Join(ConfigDir(), "backups")`
(sandbox). Make the destination track the DB so a backup of the real DB lands beside the real DB:

```go
backupDir := filepath.Join(filepath.Dir(DBPath()), "backups")   // was filepath.Join(ConfigDir(), "backups")
```

Behavior is identical in the non-override (normal) case — `DBPath()` is `ConfigDir()/endless.db`, so
its parent is `ConfigDir()`. Under the override, the real DB's backups go to the real `backups/` dir.

### 3. `cmd/endless-hook/main.go` — call it at process entry

```go
func main() {
	monitor.ForceRealDB() // E-1450: hook DB writes target the real DB, not the sandbox
	if len(os.Args) < 2 {
		...
```

Add the `internal/monitor` import to `main.go`. This runs once, before any subcommand dispatch and
well before the first `DB()` call (which happens at runtime inside `runPrompt`/`runClaude`/…). It is
intentionally *not* coupled to `claude.go`'s `init()` (the logging setup): that `init()` reads
`ConfigDir()` for the log dir, and we deliberately leave the log in the sandbox.

## What stays sandbox-routed (by design)

- **`hook.log`** — `claude.go:26` uses `ConfigDir()`; untouched. Dev-worktree hook logs stay isolated.
- **Global `config.json` (machine action-matchers)** — `internal/matchers/matchers.go:110` uses
  `ConfigDir()`; untouched. Note this is *already* absent in sandboxes (provisioning creates only
  `.sandbox-meta.json`, no config.json copy), so machine matchers are empty in sandbox today; this
  task does not change that. The core fix does not depend on matchers — `TouchSession` fires on every
  hook event (`claude.go:136`) and writes the session row regardless — so the E-1401 gate symptom is
  resolved either way. Project-level `.endless/config.json` is read via `ProjectPath(projectID)`, which
  resolves from the (now real) DB, so it stays consistent.
- **`IsSandboxActive()` gates** — e.g. snapshot-commit skip (`claude.go:958`) still see sandbox=true.

## Tests

- `internal/monitor/db_test.go` (or `sandbox_test.go`): `TestForceRealDB`
  - Simulate sandbox: `t.Setenv("XDG_CACHE_HOME", tmp)`, `t.Setenv("XDG_CONFIG_HOME", <tmp>/endless/sandboxes/worktree-e-test)`. Assert `IsSandboxActive()` is true.
  - Call `ForceRealDB()`; assert `DBPath()` now ends in `.config/endless/endless.db` (real) **and**
    `IsSandboxActive()` is *still* true (proving env was not mutated).
  - Non-sandbox case: with no sandbox routing, `ForceRealDB()` is a no-op; `DBPath()` unchanged.
  - Reset `dbPathOverride = ""` in `t.Cleanup` (package-level var; same-package test can reset it).
- Backup destination: assert `BackupDB`'s dir derivation equals `filepath.Dir(DBPath())/backups` in
  both override and normal cases (light unit test or refactor the derivation into a tiny helper).
- Regression: existing `session_test.go` uses `touchSessionDB(db, …)` with explicit fixtures and never
  touches `DBPath()` — unaffected. `go build ./... && just test`.

## Verification (end-to-end)

1. From this sandboxed worktree, trigger a hook event from a live Claude pane (any PreToolUse/PostToolUse).
2. `XDG_CONFIG_HOME=$HOME/.config sqlite3 ~/.config/endless/endless.db "select id,session_id,process from sessions order by id desc limit 5"` — the current pane's row is present (`process=$TMUX_PANE`). (Use the `endless` CLI / `endless-session-query` rather than raw sqlite3 where possible.)
3. The sandbox DB at `~/.cache/endless/sandboxes/worktree-e-1450/endless/endless.db` gets **no** new row for this event.
4. From the same pane: `XDG_CONFIG_HOME=$HOME/.config endless decision add "test" --about E-1450` — succeeds with no manual INSERT (the gate finds the hook-written row). (Run against a throwaway/expendable target, not real ledger noise.)
5. `hook.log` still lands in the sandbox (`~/.cache/endless/sandboxes/worktree-e-1450/endless/log/hook.log`), confirming DB-only scope.
6. Negative: a Claude pane in a downstream project (no sandbox routing) — `ForceRealDB()` is a no-op; writes go to the real DB exactly as today.

## Follow-ups / observations

- **E-1458** (`cleans_up E-1450`, `needs_plan`): opt-in to route the hook to the sandbox DB for
  deliberate self_dev hook testing. This design leaves a natural single seam for it — a guard around
  `ForceRealDB()` — but the opt-in mechanism itself is E-1458's to decide (its description enumerates
  env var / per-event marker / CLI wrapper, undecided) and is out of scope here.
- Machine-matcher absence in sandboxes (above) is pre-existing and orthogonal; flag for discussion
  only — file separately if it proves to bite.

## Files to modify

- `internal/monitor/db.go` — `dbPathOverride` var, `DBPath()` honors it, `ForceRealDB()`, `BackupDB()` dest.
- `cmd/endless-hook/main.go` — import `internal/monitor`; call `monitor.ForceRealDB()` first in `main()`.
- `internal/monitor/db_test.go` (or `sandbox_test.go`) — `TestForceRealDB` + backup-dir assertion.
