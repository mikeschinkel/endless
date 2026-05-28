# E-1500: Make spawn/claim tolerate an orphan task branch instead of failing

## Problem

`create_task_worktree()` (`src/endless/worktree_cmd.py:827`) — the shared helper behind both `endless task claim` and `endless task spawn` — handles only two states of the worktree **directory**:

- dir exists + belongs to this task + has companion → idempotent no-op `(wt_dir, False)`
- dir exists + foreign → `ClickException` ("does not belong")

When the dir does **not** exist it unconditionally runs `git worktree add -b <branch> <dir> <base>`, which fails when the **branch** `task/<id>-<slug>` already exists but its worktree dir is gone:

```
fatal: a branch named 'task/1444-...' already exists
```

This "orphan branch" state is produced by normal flows: `endless worktree drop` calls `git worktree remove` (removes the dir, keeps the branch); `worktree land` keeps the branch for the reaper. Any dropped, or landed-then-reaped, task leaves an orphan branch that blocks the next spawn/claim. (The specific E-1444 orphan is fallout from session 3481915d-… committing the plan to the branch under the old workflow.)

Both claim and spawn route through `create_task_worktree`, so the fix lives there and covers both.

## Decisions (settled with Mike, 2026-05-27)

No rebase is ever executed. The orphan's commits are either discardable (plan-only) or a hard stop (real work).

### Classification

Compute the branch's unique delta from base: `git diff --name-only <base>...<branch>` (three-dot — changes on the branch since the merge-base).

- **Empty delta** (branch is an ancestor of base / behind main, no unique commits) → safe. Delete the orphan branch, recreate the worktree fresh from base.
- **Delta ⊆ {`.endless/plans/E-<id>.md`}** (plan-only) → run plan reconciliation (below), then delete branch + recreate fresh.
- **Delta touches any other path** (real work — including a *different* task's plan file) → **error**. We do not auto-rebase real work; a rebase can conflict, which is the non-determinism we are avoiding.

### Plan reconciliation (plan-only orphan)

`T` = current `tasks.text` (stripped). `F` = the branch's committed `.endless/plans/E-<id>.md` content (stripped, via `git show <branch>:<path>`).

A plan is **viable** when `len(stripped) >= 128`. Empirically derived: across all task plans, every junk/placeholder entry is ≤34 chars and every genuine plan is ≥351 chars; 128 sits with wide margin between, so it rejects all observed junk and accepts all observed real plans.

| State | Action |
|---|---|
| `T` viable, `T == F` (or `F` absent) | recreate fresh; plan re-materializes from `T` |
| `T` empty/NULL, `F` viable | **adopt `F`**: recover it into `tasks.text` (emit the same text-update `task update --text` uses), then recreate fresh — DB and file now agree |
| `T` viable, `T != F` | **error** — Option-2 message (below) |
| `T` not viable (incl. `T` empty + `F` not viable) | **error** — name the problem, give the recovery command |

`tasks.text` stays the single source of truth; the committed plan file is always a derived mirror. We never silently preserve a committed plan that disagrees with the DB.

### Error messages (every path)

Every refusal names the actual problem and hands over a runnable command or two. The original `git worktree add failed: a branch already exists` (no remediation, forcing the user to debug) is the anti-pattern being fixed.

**`T != F` (Option 2 — char count + truncated preview, never a full plan dump):**
```
E-<id>: the plan in tasks.text differs from the plan committed on branch <branch>.

  tasks.text    (<N> chars): "<first ~80 chars>…"
  branch file   (<M> chars): "<first ~80 chars>…"

View full:   endless task show E-<id> --text
             git -C <root> show <branch>:.endless/plans/E-<id>.md
Keep DB version, discard branch:
             git -C <root> branch -D <branch>          # then retry
Adopt branch version into the DB:
             git -C <root> show <branch>:.endless/plans/E-<id>.md > /tmp/E-<id>.md
             endless task update E-<id> --text /tmp/E-<id>.md   # then retry
```

**Real-work orphan:**
```
E-<id>: branch <branch> has commits beyond <base> touching non-plan files:
  <file1>
  <file2>
Inspect:  git -C <root> log <base>..<branch>
          git -C <root> diff <base>...<branch>
Resume that work manually, or discard it and retry:
          git -C <root> branch -D <branch>
```

**`T` not viable:**
```
E-<id>: tasks.text is too short to be a viable plan (<N> chars):
  "<preview>"
Write a real plan, then retry:
  endless task update E-<id> --text <file>
[If F is viable: the branch's committed plan (<M> chars) may be the one you want — see `git -C <root> show <branch>:.endless/plans/E-<id>.md`.]
```

### Secure the worktree before flipping status

`_perform_claim_work()` (`task_cmd.py:2270`) currently emits `task.status_changed` (ready→in_progress) **before** calling `create_task_worktree`. So when worktree creation refuses, the task is left stuck `in_progress` — exactly what happened to E-1444 in the bug report. Reorder: create the worktree first; emit the status flip and any `task.claimed` binding only after it succeeds. A refusal then leaves status untouched.

### Stale / prunable git registration

If `git branch -D` reports the branch is "checked out" in a worktree whose directory is already gone (a prunable registration), run `git worktree prune` (clears bookkeeping only for already-missing dirs — touches no live work) and retry the delete once. If it still fails, refuse with the exact `git worktree prune` / `git worktree remove` command.

## Out of scope

- **Live worktree adoption** (dir exists + belongs to task): unchanged — still the idempotent no-op. `task update --text` already mirrors into a live worktree; re-materializing there could clobber in-progress edits.
- **Ownership gate** (goal_task_id consultation, tmux switch command, dead-pane notice): E-1501, blocked by E-1483.
- Migration / back-compat: endless has one user, no shipped product.

## Files to modify

- `src/endless/worktree_cmd.py` — orphan-branch detection + classification + reconciliation inside `create_task_worktree`; new helpers `_branch_exists`, `_branch_unique_files`, `_read_branch_file`, `_plan_viable`, `_delete_orphan_branch` (with the prune retry), and the error-message builders. Add module constant `PLAN_VIABILITY_MIN_CHARS = 128`.
- `src/endless/task_cmd.py` — reorder `_perform_claim_work` so `create_task_worktree` runs before the status-flip / claimed events. The adopt-`F` path recovers `F` into `tasks.text` via the existing `task update --text` emission, threading the resolved session/db context so it does not trip the E-1401 attribution gate during spawn pre-claim.

## Tests (`tests/test_worktree_orphan_branch.py`, plus a threshold unit test)

Using the `project_with_task` / `seeded_project_at_cwd` fixtures (real git repo + DB), build an orphan branch by creating `task/<id>-<slug>` from main, optionally committing a plan file, then removing/never-creating the worktree dir, then call `claim_item` / `create_task_worktree`:

1. **plan-only, T == F** → worktree recreated, branch exists, succeeds.
2. **empty delta (branch ancestor of main)** → recreated, succeeds.
3. **plan-only, T != F (both viable)** → raises; message contains both char counts, `endless task show … --text`, `git … show <branch>:…`, `git branch -D`, `endless task update … --text`. Branch preserved (not deleted on the error path).
4. **plan-only, T empty, F viable** → tasks.text recovered to F; worktree recreated; materialized plan == F.
5. **plan-only, T empty, F not viable** → raises "no viable plan."
6. **plan-only, T not viable (<128)** → raises "too short."
7. **real-work delta (commit touches a .py file)** → raises; message lists the file + `git log` / `branch -D`. Branch preserved.
8. **status untouched on refusal** → real-work orphan: `claim_item` raises and task status is still `ready`.
9. **threshold unit test** → `_plan_viable` False at 127 chars, True at 128.

## Verification

1. Real E-1444 orphan: `endless task spawn 1444` (or `claim`) now succeeds, recreating `.endless/worktrees/e-1444` and re-materializing the plan from `tasks.text` (T == F there).
2. `just test` green (Python).
3. Manual: `endless worktree drop <id>`, then `endless task spawn <id>` — succeeds (orphan branch recovered).
4. Manual real-work case: create a branch with a code commit and no dir, attempt spawn — refuses with the file list + commands; task status unchanged.

## Closeout

Set status `verify` with the how-to-test above. Do not land; ask Mike.
