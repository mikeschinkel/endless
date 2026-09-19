# E-1643 — Explore guarding the one-worktree-per-task invariant

**Independent. Research/policy task — the deliverable is a recommendation in `outcome`, not code.**

## Question
Today a task may spawn additional worktrees at `.endless/worktrees/e-<id>-<slug>/`
(`WorktreePathForTask`, `internal/monitor/worktree.go:15-54`, globs both `e-<id>` and `e-<id>-*`).
Mike wants to lean toward **one worktree per task** and model genuine multi-checkout needs as
**child tasks**. Should we discourage / warn / forbid alt worktrees?

## What to investigate
1. **Where alt worktrees get created/allowed:** `worktree_cmd.py:1055-1121`; the `e-<id>-*` glob
   in `worktree.go:15-54`; anywhere assuming >1 worktree per task.
2. **Legitimate multi-checkout needs and the child-task model that replaces them:** A/B-ing two
   implementations → two child tasks + a "compare and pick" child; run-while-edit → child task;
   bisect/snapshot-compare → child task. Assess whether child tasks cleanly cover each case.
3. **What breaks if alt worktrees are forbidden/discouraged:** tests, `@endless_task_id` binding,
   `worktree land`/`drop` semantics, dashboard assumptions.
4. **Interaction with respawn (E-1644):** respawn follows the session→worktree binding, so it's
   correct either way; if alt worktrees go away, respawn always targets the primary `e-<id>`.

## Deliverable
Write the recommendation to this task's `outcome`: **forbid / warn-only / leave as-is**, with
rationale, code touch-points, and a migration note. Any actual guard/enforcement becomes a
separate follow-on task.

## Verification — create a per-task verify script
This is a **research task with no code deliverable**, so the verify script is intentionally
minimal — a research outcome is reviewed by *reading* it, not by assertions. Create
`tests/tasks/e-1643-verify.sh` in the `e-1624-verify.sh` shape (bash, `set -u`, the
`section`/`report_*`/`summary` helpers, exit 0/1/2) that asserts only the deliverable exists:
- `endless task show E-1643 --outcome` is **non-empty** and names one of `forbid` / `warn` /
  `leave` (a concrete recommendation was recorded, not an empty hand-off).

If E-1643 instead spawns a follow-on guard/enforcement task, that task carries its own real
verify script; this one just guards against an empty research hand-off. Run it twice to confirm
it's re-runnable.

**Verify-handoff — tell the user to verify with (only) this:**
```
esu && ./tests/tasks/e-1643-verify.sh
```
