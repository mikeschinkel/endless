# E-1703 — Refuse write-tool targets outside the bound task's worktree

Blocked by **E-1714** — build only if E-1714 confirms `enforceClaimedCwd` fires
(i.e. the incident was this target-path case, not an E-1586 regression).

## Gap

Both existing PreToolUse gates are **cwd-based** and neither validates the write
target:
- `enforceWorktreeGate` (E-971) resolves the worktree from `payload.CWD`.
- `enforceClaimedCwd` (E-1586) checks `payload.CWD` against the bound task's
  worktree, and its message even invites "reach another directory by passing an
  explicit absolute path" (claude.go:1434; `enforceWorktreeGate` says the same at
  1373).

So a claimed session sitting correctly inside its worktree can `Write`/`Edit`
the main checkout via an absolute path with nothing to stop it.

## Behavior

When a session has a **non-terminal claimed task whose worktree exists**, a write
tool (`Write`/`Edit`/`NotebookEdit`) whose resolved target path is **not** inside
that worktree is refused. Reads and Bash to outside stay allowed (reading main is
fine) — only write *targets* are constrained.

Runs **unconditionally** (independent of `tracking_mode`), matching its siblings
`enforceClaimedCwd`/`enforceWorktreeGate`, which both run before the tracking-mode
check. No new `checks.go` key.

## Implementation

- Factor the common preamble out of `enforceClaimedCwd` into a shared helper
  `sessionOwnedWorktree(projectID, payload) → worktreePath` (active session →
  non-terminal active task → `WorktreePathForTask` → lock-owner guard; returns ""
  when any precondition fails).
- New sibling `enforceClaimedWriteTarget(projectID, payload)`, called in the
  write-tools block next to `enforceWorktreeGate` (~claude.go:487):
  ```go
  if !writeTools[payload.ToolName] { return }
  wt := sessionOwnedWorktree(projectID, payload); if wt == "" { return }
  target := extractFilePath(payload.ToolName, payload.ToolInput)
  if target == "" { return }
  if !filepath.IsAbs(target) { target = filepath.Join(payload.CWD, target) }
  if !pathWithin(wt, target) { blockToolUse(writeTargetRedirect(taskID, wt, target)) }
  ```
  Reuses existing `extractFilePath` / `pathWithin`. `enforceClaimedCwd` keeps its
  single (cwd) purpose and now calls the shared helper too.
- New `writeTargetRedirect(taskID, worktreePath, target)` message (distinct from
  `cdRedirect`, which is about cwd): says the target is outside the worktree, that
  every edit for this task must land inside it, and to re-target the path under the
  worktree — and that if this is genuinely main-work, this isn't the task for it.
  Paths rendered via `tildePath`; literal worktree path kept absolute for paste.

## Message-consistency fix (confirmed)

Reword the "reach another directory by passing an explicit absolute path" lines in
`cdRedirect` (1434) and `enforceWorktreeGate` (1373) to scope that to **reads**:
"...for reads; writes must stay inside the worktree." So the guidance no longer
contradicts this gate.

## No `--force` bypass (decision)

Deliberately no per-write override. It would violate "gates, not guardrails"
(bypasses get taken), and would be taken in exactly the accidental case the gate
targets (the agent always believes its write is intended). There's also no
tool-level flag to hang it on — it would require a stateful "arm the next write"
command that is itself a standing bypass.

**Non-goal / future escape:** the genuine "write outside the worktree" case
(co-developed deps under `../go-pkgs/*`) is better served by a durable,
Mike-reviewed **allow-list of extra writable roots** (per-task/per-project), set
deliberately — not an in-the-moment agent override. Deferred; tie to **E-1085**
if it becomes a real need.

## Boundary (per E-1712)

Fires only when a non-terminal task *with a worktree* is bound. No-task /
terminal-task / unclaimed sessions and deliberate direct-to-main work
(global-config artifacts, ledger hygiene) are exempt, unchanged.

## Tests

- Unit: target in worktree → allow; target in main → block; relative target
  resolved against cwd → block/allow correctly; non-write tool → ignore; no
  claimed task / terminal task / no worktree → ignore; lock owned by another
  session → ignore.
- `tests/tasks/e-1703-verify.sh`: claimed session with cwd in its worktree —
  write-to-main blocked, write-in-worktree allowed, read-main allowed.
