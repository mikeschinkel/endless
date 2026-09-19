# E-1645 — Revise `task spawn --reopen`: liveness guard, ensure/rebuild worktree+sandbox, inherit-session

**Epic:** E-1644 · **Blocked by:** E-1647 (landed/assumed) · **Layer:** Python (`task_cmd.py`, `cli.py`) + a Go resolver (`internal/events` + `internal/sessionquerycmd`).

## Context

`endless task spawn --reopen` exists (E-1555) but is suboptimal: it flips a terminal
task back to `ready`/`needs_plan`, then spawns with the **per-type** initial-spawn handoff —
no liveness navigation, no session inheritance, no explicit sandbox rebuild, and it renders the
wrong (drive-to-end-state) handoff instead of the interrogative respawn handoff E-1647 just
landed. This task makes `--reopen` the real "reopen core" so a reopened session (a) never
double-spawns over a live owner, (b) lands in a present-or-rebuilt worktree with a working
sandbox, (c) inherits the most-applicable prior *ended* session by default (skipping sub-10s
ghosts), and (d) gets the flat `handoff/respawn` template. Structured so E-1646 (`task respawn`
/ `session respawn` verbs) can reuse the same decision core.

## Decisions locked with Mike (2026-06-24)

- **`--new-session` + existing worktree → reuse if present.** `--new-session` only opts out of
  *prior-session context inheritance* (outcome/snapshot). The worktree is still reused-if-present,
  rebuilt-off-main only if reaped. It never destroys an existing worktree (could hold local state).
- **The verify seam is truly read-only and named `--print-decision`** (NOT `--dry-run`, which
  falsely implies a no-op-but-otherwise-full run). It computes and prints the reopen decision —
  including the *predicted* `restore_case` from whether the worktree dir exists — with **no
  mutations** (no status flip, no worktree/sandbox creation, no launch). The actual
  worktree+sandbox rebuild stays covered by `create_task_worktree`'s own behavior/tests.
- **Branch-collision on reaped-but-branch-exists (drop case): reuse existing behavior.**
  `create_task_worktree` → `_handle_orphan_branch` (`worktree_cmd.py:1090-1091`) already reuses
  the `task/<id>-<slug>` branch when it carries no real work and raises when it carries non-plan
  work. No new policy; document the reliance.

## Architecture

Extract a **pure decision function** so both the seam and the live path compute identically, and
E-1646 can reuse it:

```
_resolve_reopen_decision(item_id, *, new_session) -> ReopenDecision   # READ-ONLY
```

`ReopenDecision` (a small dataclass / dict) carries:
- `kind`: `"navigate"` | `"spawn"`
- `navigate`: `None` | `{"target": "fg", "pane_id": "%NN"}` | `{"target": "bg", "task_id": N}`
- `restore_case`: `"reused"` | `"rebuilt-off-main"` (predicted from `wt_dir.exists()`)
- `worktree_path`: resolved canonical path (`.endless/worktrees/e-<id>`)
- `inherit_session_id`: `int | None` (None when `new_session=True` or no eligible prior session)
- `session_mode`: `"inherit"` | `"new-session"`

It performs only reads: the liveness lookup (existing `_live_sessions` + `_lookup_bg_short_id`)
and the Go resolver call. No status flip, no worktree creation.

## Behavior changes (in `spawn_plan`, `task_cmd.py`)

### 1. Liveness guard → navigate (reopen only)
Compute the decision *first*, before any mutation. If `kind == "navigate"`:
- **Foreground** (live session has a `%NN` pane): `tmux switch-client -t <pane>`
  (**`switch-client`, not `select-window`** — works across tmux clients). Print what happened,
  return without spawning.
- **Background** (kind `background`, no pane): print `attach with: endless task attach E-<id>`,
  return.
- `--new-session` does **not** bypass a live owner.

Reuses the data `_check_task_ownership` already gathers (`task_cmd.py:2634-2677`,
`_live_sessions`/`_project_root_for_cwd` in `session_cmd.py`); replaces *raise* with *navigate*
for the reopen path only. Non-reopen spawn keeps the existing `_check_task_ownership` raise.

### 2. Ensure / rebuild worktree + sandbox
Already provided by `_perform_claim_work` → `create_task_worktree` (`worktree_cmd.py:1055-1121`):
present → reuse (`created=False`); reaped → fresh branch off `main` + `_maybe_auto_sandbox_bind`
re-provision+bind (`worktree_cmd.py:1120, 1214-1262`). The reopen path derives the **actual**
`restore_case` from the returned `created` flag (`False` → `reused`, `True` → `rebuilt-off-main`)
and passes it to the handoff render. No new worktree code.

### 3. Inherit the most-applicable prior ended session (default)
New **Go resolver** (live-first handled by the guard; this picks among *ended* rows):

```sql
SELECT id FROM sessions
WHERE active_task_id = :task AND state = 'ended'
ORDER BY (process IS NOT NULL OR transcript_path IS NOT NULL
          OR (julianday(last_activity)-julianday(started_at))*86400 >= 10) DESC,
         started_at DESC
LIMIT 1;
```
- **Skips sub-10s ghosts; never `ORDER BY dur DESC`** (Pattern B would let a stale row span days).
  Defends against E-1640 ghosts whether or not E-1640 has landed.
- `--new-session` → resolver not consulted; `inherit_session_id=None`, fresh session row
  (created naturally by the spawned session's SessionStart hook) + reused/rebuilt worktree.

### 4. Render the flat respawn handoff
The reopen path renders with `respawn=True` and the three vars (fg call ~`task_cmd.py:4144`, bg
call in `_spawn_bg_dispatch` ~`task_cmd.py:4333`). E-1647's `render_handoff(respawn=…,
restore_case=…, prior_outcome=…, last_status_snapshot=…)` already accepts them. Keep fg + `--bg`
parity. `prior_outcome` + `last_status_snapshot` come from the Go resolver (below).

## Go resolver (new)

- **`internal/events/reopen_context.go`** — `func ReopenContext(taskID int64) (ReopenContext, error)`.
  Placed in `events` because it reuses `monitor.DB()` (events already imports monitor) **and** the
  unexported `renderSessionStatusMarkdown` (same package) — no new import cycle. Returns:
  - `InheritedSessionID int64` (0 = none) — the ghost-skip query above.
  - `PriorOutcome string` — `tasks.outcome` for the task (`""` if null).
  - `LastStatusSnapshot string` — latest `session_statuses` row for the inherited session, rendered
    via `renderSessionStatusMarkdown` (`""` if none/no inherited session).
- **`internal/sessionquerycmd/session_query.go`** — add subcommand
  `reopen-context --task-id <id>` (switch case ~line 50 + `runReopenContext`, mirroring
  `runTaskText` at 176-191), emitting the struct as JSON to stdout. Update `usage()` + the
  `main.go:14` doc comment.
- Python calls it from the decision/render path via the existing
  `[binary, *config.go_db_context_args(), "session-query", "reopen-context", "--task-id", str(id)]`
  shell-out pattern (sandbox-routing-safe). Keeps all *new* DB reads Go-side per E-894/E-1486.

## CLI (in `cli.py:1559-1598`)

Add two options to `task spawn`:
- `--new-session` (is_flag) — fresh session row, no prior-session inheritance. Meaningful only
  with `--reopen`; raise if passed without it.
- `--print-decision` (is_flag) — read-only: print the resolved reopen decision (navigate vs
  spawn, predicted `restore_case`, `inherit-session=<id>` vs `new-session`, worktree path) and
  exit. No tmux/Claude, no mutations. Reopen-path scoped.

Thread both through `spawn_plan(... new_session=False, print_decision=False)`.

## Files

- `src/endless/task_cmd.py` — new `_resolve_reopen_decision()`; rework the `reopen` branch in
  `spawn_plan` (3955-4226) to: compute decision → navigate-or-return → (if `--print-decision`)
  print+return → `_reopen_task_core` → `_perform_claim_work` (capture `created`) → render with
  `respawn=True`+vars (fg + `_spawn_bg_dispatch`). Small helper to render the decision line.
- `src/endless/cli.py` — two new options + docstrings + pass-through.
- `internal/events/reopen_context.go` — **new** resolver + JSON struct.
- `internal/sessionquerycmd/session_query.go` — **new** `reopen-context` subcommand + usage.
- `cmd/endless-go/main.go` — doc-comment line listing the new subcommand.
- (No changes to `worktree_cmd.py`, the templates, or `render_handoff` — E-1647 already shipped them.)

## Verification — `tests/tasks/e-1645-verify.sh`

New script in the `e-1624`/`e-1647` shape (bash, `set -u`, `section`/`report_pass`/`report_fail`/
`summary`, `cd` repo root via `git rev-parse --show-toplevel`, ensure `go.work` (regen via
`just go-work-init`), build `bin/endless-go` on demand, exit **0 all-pass / 1 any-fail / 2
env/setup**). Named checks:

- **Resolver skips ghosts (Go-level, exact):** a Go unit test in `internal/events` seeds (fresh
  ids) one sub-10s ended ghost + one ≥10s ended real session on a task in a temp DB; asserts
  `ReopenContext` returns the **real** session's id. Script runs it with `go test -run`.
  Plus a **binary** check: `endless-go --config-dir <tmp> session-query reopen-context --task-id`
  against a seeded temp DB returns the real id in JSON.
- **`--new-session` decision:** drive `endless task spawn E-<id> --reopen --new-session
  --print-decision` against a seeded sandbox task (terminal status); assert the printed decision
  is `new-session` (not `inherit-session=…`).
- **Reaped worktree → predicted rebuild:** with the canonical worktree dir absent,
  `--reopen --print-decision` reports `restore_case=rebuilt-off-main`; with it present, `reused`.
  (Read-only seam: asserts the *decision*, not filesystem creation.)
- **Liveness guard:** seed a live **background** session (via `endless-go session-query
  record-bg-agent`, which inserts a working bg row), then `--reopen --print-decision` → decision
  `navigate` (`attach`), NOT spawn. The **foreground** `switch-client` side-effect stays a
  documented manual check in the script header (per the plan).

Re-runnability (sandbox NOT wiped between runs): derive all seeded task/session ids from
freshly-allocated ids each run (never a fixed UNIQUE value); capture seeding output and `exit 2`
loudly on any setup/seed error (never `/dev/null`). Run the script ≥2× before declaring done.

**Verify-handoff — tell Mike to verify with (only):**
```
esu && ./tests/tasks/e-1645-verify.sh
```

## Build / land

`just build` (templ+css+go+py) after edits — stale Go binaries reject new flags. Do NOT
`worktree land`/`drop` (spawning session owns landing). When done: `endless task update E-1645
--status verify --db main`, then hand back with the verify line + return anchor.
