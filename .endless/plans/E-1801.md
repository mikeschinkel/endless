# session resume: recover a dropped worktree via `--review` / `--reopen`

## Problem
`endless session resume <ref>` cds into the target's worktree and execs
`claude --resume <uuid>`. It assumes the worktree still exists on disk. When a task lands
and its worktree is dropped, the transcript survives but the directory is gone, so
`_resolve_resume` dead-ends at `if not os.path.isdir(worktree): raise ...worktree path does
not exist` (`session_cmd.py:184`). The session is fully recoverable (transcript intact) but
no directory maps to its resume bucket. Motivating case: E-1202 — landed via fast-forward,
branch deleted, worktree gone; the goal was only to reopen the session to verify the landed
result.

## Design — two intent flags (+ shared `session goto --resume` resolver)
The verb names the intent; the user never needs to type a git ref. Both flags recreate the
dropped worktree from the surviving transcript. Behavior is a function of the task's
**state** — type, landing history, branch presence, and status — in that order.

### 1. Type gate (checked first)
- **`epic`** → refuse both flags: an epic is a container with no worktree/session of its
  own; direct the user to a child task.
- **`todo` / `bugfix` / `research` / `brainstorm`** → all recoverable. Every one lands (its
  db-ledger entries land into main even when no code changed), so `.landed` resolves for all
  of them normally. Type does **not** gate landing — only `epic` is special.

### 2. Recovery base (landing? / branch?)
- `.landed` = the `merge_commit_sha` of the task's **most recent** `task_landings` row, if
  the task has landed; else
- the original branch tip, if the branch still exists; else
- error, unless an explicit `--review=<ref>` / `--reopen=<ref>` override is given.
This fallback covers the genuine edge of a task that **never landed** — a landing-history
fact, not a type property.

### 3. `session resume <ref> --review`
- Recreate the worktree **detached** at the recovery base (default `.landed`; override via
  `--review=<ref>`).
- **No status change, in any state.** Read-mostly: inspect/verify a finished task, run its
  verify script against the landed tree.
- cd + exec `claude --resume <uuid>` (unchanged tail).

### 4. `session resume <ref> --reopen`
- Recreate the worktree on a **working branch**: reuse the original branch if it exists,
  else a fresh branch off the recovery base (default `.landed`; override via
  `--reopen=<ref>`).
- **Status transition depends on the task's current status:**

| Current status | transition |
|---|---|
| confirmed / assumed / completed (done) | → `revisit` |
| declined / obsolete (rejected) | → `revisit` |
| underway / unverified (still active) | no change — restore tree only |
| unplanned / submitted / ready / revisit (open) | no change — restore tree only |

- cd + exec `claude --resume <uuid>`.

### 5. `.landed` pseudo-ref
Resolves to the latest landing's `merge_commit_sha`. It is the **default base the resolver
uses when no ref is given** (the user types nothing), and also a **typeable override value**
(`--review=.landed`). Collision-safe: git's `check-ref-format` forbids any refname from
beginning with `.`, so `.landed` can never clash with a real branch/tag and lives safely in
the same value space as explicit refs. Scope it to the resume resolver — not a global
git-ref shim.

### 6. Unchanged / error paths
- Bare `session resume`, worktree present → unchanged (tmux-crash recovery, tree intact).
- Bare `session resume`, worktree gone → replace the bare `worktree path does not exist`
  error with one recommending both flags: "worktree gone; use --review to inspect the
  landed result, or --reopen to continue work on it."

## Implementation touchpoints
- `_resolve_resume` (`session_cmd.py:160`) is shared by `resume_session` (current pane) and
  the `goto --resume` path (`session_cmd.py:2058`). Add the worktree-missing branch here so
  both surfaces behave identically.
- **Decouple worktree creation + bootstrap from `task claim`'s status side effects:** factor
  the bootstrap (go-work-init, bin copy, claude-settings-init, dev-sandbox-init) into a
  shared helper; each caller owns its own status transition (`claim`→underway, `--reopen`→
  per the table, `--review`→none). `--review` requires this decoupling since it changes no
  status.
- `--review` uses `git worktree add --detach <path> <base>`; `--reopen` uses a branch
  (reuse original, else fresh off the base).
- Resolve `.landed` from the latest `task_landings.merge_commit_sha`.
- Keep the resumed-session tail (`os.chdir` + `os.execvp`) as-is.

## Verification
Single `tests/tasks/e-1801-verify.sh` on the established convention (shape per
`tests/tasks/e-1577-verify.sh`: `set -u`; `section`/`report_pass`/`report_fail`/`summary`;
`cd` to `git rev-parse --show-toplevel`; `endless() { uv run endless "$@" --db sandbox; }`;
exit 0 all-pass / 1 on any failure). Hand off exactly `esu && ./tests/tasks/e-1801-verify.sh`.
Each case drives a real land + worktree drop in the sandbox and asserts worktree recreation,
status, and ref resolution — **stopping before** the `claude --resume` exec (the tail
`execvp`s claude and isn't asserted):
1. Landed done task, worktree dropped → `--review` recreates **detached** at the latest
   `merge_commit_sha`; status unchanged.
2. Same → `--reopen` recreates on a **branch**; status → `revisit`.
3. `--reopen` on an **underway** task (worktree dropped) → restored; status unchanged.
4. `--review=<explicit-sha>` and `--review=.landed` both resolve; with **multiple** landings,
   `.landed` = the latest.
5. **`epic`** → both flags refused with a child-redirect message.
6. Bare resume, worktree gone → improved error naming both flags.
Plus unit coverage exercising the resolver's base/status decisions across the state matrix
(mock `task_landings` rows + branch existence).

## Relationship
Relates to E-1776, E-1797.
