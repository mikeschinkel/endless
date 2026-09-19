# E-1586 — Force /cd into your worktree before acting (cwd-invariant gate)

## Problem

`endless task claim` / `spawn` give the session a live, owned worktree, but
Claude Code's **harness cwd** starts at the project root. The shell moves the
guide documents (`cd "$(endless worktree for-task)"`, `esu`/`eswt`) only change a
Bash/interactive cwd — not the harness cwd that `Write`/`Edit` and a fresh `Bash`
resolve against. So until the agent runs `/cd`, tool uses default to **main's
working tree**. `/cd` (newly shipped) moves the harness cwd once; this gate makes
doing so non-optional.

## Design (FINAL — simplified through design discussion 2026-06-16/17)

**No allowlist.** Enforce one invariant: a session that *owns a live worktree*
must have cwd == that worktree. Block tool use when cwd has drifted out; remedy
is `/cd <worktree>`. Once cwd == worktree, nothing is blocked — a tool can still
reach another directory via an explicit absolute path. We only catch the
*default-cwd* footgun (the path of least resistance silently being main).

### Keying signal: non-terminal active-task status (NOT lock ownership)

Implementation note (corrected during build): the worktree **lock** is claimed
by a `SessionStart` *inside* the worktree (worktree adoption), which does **not**
fire for the common "claim in main, then `/cd`" flow — so requiring lock
ownership would leave that flow ungated. Instead the gate fires when the
session's **active task is non-terminal** (the status `claim` sets is
`in_progress`) and its worktree exists and cwd is outside it. This cleanly
excludes:
- **`bind`** of a done task — status terminal (`confirmed`/`assumed`/…) →
  ignored (correct: bind is display-only).
- **Landed/retained worktrees** (reaper not yet run) — task is terminal →
  ignored, no spurious redirect into a stale dir.

The lock is still consulted, but only to **avoid redirecting into a worktree a
different live session owns** (`ReadWorktreeLock` + `IsWorktreeLockStale`).
Terminal set: `{confirmed, assumed, declined, obsolete, completed}` via
`monitor.IsTerminalTaskStatus`.

### Scope: claimed sessions only — do NOT brick main

Only fires for a lock-owning session whose cwd has drifted. A taskless session in
main stays free (`just install`, `git`, `endless …`). Preserve the existing
E-971 write-to-main protection (the generic "edits in main discouraged" with no
active task) as **write-tools-only** — do NOT extend that branch to Bash, or the
main checkout becomes unusable from a shell.

### Build on existing code

`enforceWorktreeGate` (claude.go:1110) already does the main/ownership/cwd checks
for **write tools**, and its remedy text is literally the old workaround ("Run
`cd <wp>` in a Bash call … use absolute paths"). E-1586 = (1) extend the gate
past write tools to Bash + the rest (default-deny by cwd, no tool allowlist), and
(2) replace that remedy text with `/cd <worktree>`. Block message uses
home-relative paths; the literal `/cd <path>` stays absolute for paste-safety.
No internal endless ticket IDs in the message (the active task's own E-NNN is
fine — it's the user's task).

## Verb model — SETTLED, no changes

`claim` / `reopen` / `bind` all stay as-is; each is verified distinct:
- `claim` (`--force` to reactivate) — work it here: bind + worktree + `in_progress`.
- `reopen` — reactivate **unbound** for handoff/pool (E-1555); `claim` cannot do
  this because claiming binds and blocks `spawn`.
- `bind` — display only: set `active_task_id`, no worktree, no status (E-1256).

No bind redesign (the worktree-making `bind` idea is dropped). The
bug-fix-after-land case is `claim --force`. `--force` is kept as a deliberate
error gate (catches "didn't know it was done" / typo'd IDs). Whether `bind`
should ever change status is the separate **maybe** task E-1587 (`--status`
flag), to be resolved by concrete flows.

## Rollout (the three)

1. **Hook gate** — `internal/hookcmd/claude.go` (extend `enforceWorktreeGate`).
2. **Guide** — document `/cd` in happy-path step 3 of `docs/guide/index.md`
   ("Get into the worktree") as the primary move; add a shell-helpers row in
   `docs/guide/orchestration.md` distinguishing `/cd` (harness cwd, all tools)
   from `esu`/`eswt` (interactive shell + `ENDLESS_SESSION_ID` routing). No
   `E-NNNN` in shipped docs.
3. **Claim-output nudge** — emit the `/cd "$(endless worktree for-task <id>)"`
   directive from `claim` (`src/endless/task_cmd.py`); coordinate with E-1428.
