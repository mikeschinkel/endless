# Two groups, both left out of E-2159 deliberately, both fully specified here.

## A. Two agent-facing messages still offer `git branch -D` where it destroys real work

`endless task claim` refuses when an orphan branch for the task already
exists, and three of its refusals print `git branch -D <branch>` as a
closing command. One of the three is correct and stays as it is; two are
destructive escapes that belong in `human_remedy`.

Correct, do not change — `_handle_orphan_branch`'s post-prune failure
(src/endless/worktree_cmd.py, the `agent_help.report` after `worktree
prune`): the branch was proven to hold no unique work before anything
tried to delete it, so `branch -D` there destroys nothing. It is the
remedy, not a bypass. Its comment already says so.

To change:

1. `_orphan_real_work_msg` (src/endless/worktree_cmd.py) — the branch
   holds commits touching files no document mirror accounts for. That is
   work nothing else has a copy of; `branch -D` is irreversible loss.
2. `_orphan_mirror_mismatch_msg` (src/endless/worktree_cmd.py) — the
   branch holds mirror content the database does not have, so the branch
   is the only place that text exists. The message says as much, then
   offers the command that erases it.

Both already carry a comment saying the command "would belong in
`human_remedy`"; both say it stays only because a test asserts on it.
Move each closing `branch -D` into `human_remedy`, which keeps a person's
bytes identical and removes the bypass for an agent — the one inversion
the E-2159 helper is designed for.

The blocker, and it is the whole reason this is a separate task:
tests/test_worktree_orphan_branch.py asserts
`f"branch -D {p['branch']}" in msg` against the agent-facing render, in two
places — `grep -n "branch -D" tests/test_worktree_orphan_branch.py` finds
both. Both assertions must move to the human render. Re-point them at the text a
person sees and add the matching negative — that the agent's copy does
NOT contain `branch -D` — so the inversion is pinned in both directions
rather than merely relaxed.

## B. Three warnings that block nothing and only the user can clear belong on the errors channel

E-2159 moved the warnings its plan named. These three fit the same rule
— nothing is blocked, the user is the only one who can act — and were
left out because each needs a catalog code and a `docs/errors.md`
section, which is a unit of work in itself.

1. post-worktree-create hook present but not executable
   (`_run_post_worktree_create_hook` in src/endless/worktree_cmd.py; find it
   with `grep -n "post-worktree-create hook .* is not executable"
   src/endless/worktree_cmd.py`). The hook is skipped; the
   worktree is fine. Only a `chmod` fixes it, and only the user can do
   that.
2. post-land script present but not executable
   (the `post-land script ... is not executable` message in
   src/endless/worktree_cmd.py). Same shape, same remedy.
3. stale companion session (src/endless/worktree_cmd.py,
   src/endless/session_cmd.py). Confirm the exact sites before writing
   the code: the string appears in both files and only one may still be
   reachable.

For each: `agent_help.warn.record` with a new code, and a
`docs/errors.md` section whose "What to do." paragraph is byte-identical
to the catalog `Remedy` — `go test ./internal/faults/` enforces that
equality. Take the next free numbers in sequence and never reuse a
retired one; WARN-0021..0025 are spent by E-2159, and ERR-0015 is
E-1887's, so a collision check against both catalogs comes first.

Keep the ⚠ line a person sees. The errors channel is additive: it puts
the warning on the session-status badge and in `endless errors show` so
it survives the scrollback, and it costs an agent nothing.

## Verification

Fold into this task's verify.sh: the two negative assertions from A (an
agent's copy of each message carries no `branch -D`) and, for B, the
`internal/faults` test plus one assertion per new code that the warning
reaches `endless errors show`.
