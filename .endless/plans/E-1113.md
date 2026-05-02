# E-1113: Design event-commit workflow for non-land merge paths

## Current state

`.endless/events/*.jsonl` files are append-only event logs that **must be
committed to git** (per `docs/guide-2026-04-15-using-endless-in-sessions.md` /
quick-start: "clone-completeness means task state travels with the repo").

Today the only path that commits them automatically is
`endless worktree land`, which auto-commits any dirty paths matching
`AUTO_COMMIT_GLOBS` in `src/endless/worktree_cmd.py:48`:

```python
AUTO_COMMIT_GLOBS = (
    ".endless/events/*.jsonl",
    ".endless/plans/snapshots/*",
    ".endless/config.json",
)
```

The land flow partitions `git status` into auto-commit-files vs user-work-
files, commits the auto-commit set as a separate commit, then proceeds with
the worktree's own commits.

## The gap

Anyone who lands a feature branch via `git merge`, `git rebase`, `git push`,
or any direct git operation (rather than `endless worktree land`) leaves
events files dirty. They accumulate untracked indefinitely.

Concrete observed example: this session merged E-1088, E-1106, E-1111 to main
via `git merge --no-ff`. After all three landed, main still showed:

```
M  .endless/events/events-a7f3-000001.jsonl  (this session's appends)
?? .endless/events/events-1421-000001.jsonl  (this session's new file)
```

A future clone or sync would miss these task-creation events entirely.

## Stakes

- **Clone-completeness**: a fresh clone must reproduce the same DB
  projection. Missing events break this contract.
- **Distributed development**: multi-machine syncs (or future multi-user
  scenarios) depend on events being present in git.
- **Silent failure mode**: nothing warns the developer that events are
  uncommitted. The longer they sit, the more events accumulate, and the
  riskier any "rebase to clean up" maneuver becomes.

## Design options

Each has trade-offs. Pick one or combine.

### A. Auto-commit on every event write

Every `endless task *` / `endless channel *` / etc. that emits an event
also makes a tiny git commit immediately afterwards.

- **Pro**: never loses an event; simplest mental model.
- **Con**: noisy git history (one micro-commit per event); `git log` becomes
  unreadable; commits race with the session's actual work commits; potential
  conflicts during interactive rebase.

### B. Auto-commit periodically (timer / batch)

A daemon or hook commits accumulated event files every N minutes (or N
events).

- **Pro**: less noisy than (A).
- **Con**: requires a long-running process; still produces a separate event
  commit stream that interleaves with feature commits; bounding latency vs.
  noise is a tuning problem with no good answer.

### C. Auto-commit at session end

When a Claude Code session ends (or `endless task verify` is run, or a hook
fires on shell exit), commit accumulated events.

- **Pro**: feels natural — events are committed when the session that
  created them concludes.
- **Con**: detecting "session end" reliably is hard (Ctrl-C, crash, kill,
  laptop sleep). Without reliable detection, this is just (B) by another name.

### D. Wrap git operations on the main checkout

Hook into `git merge`, `git rebase`, `git push` (via git hooks or a
shell wrapper) so they auto-commit events the same way `endless worktree
land` does.

- **Pro**: aligns merge path with land path; events get committed at the
  natural integration boundary.
- **Con**: shell hooks are easy to bypass; git hooks live in `.git/hooks`
  which isn't committed (each clone needs setup); pre-merge hook timing
  is fiddly.

### E. Block direct git merge / rebase on main, force land path

`endless-hook` already blocks direct `git commit` on main's working tree
(see `cmd/endless-hook/claude.go:347` — comment references E-1012). Extend
to block direct `git merge` / `git rebase` on main, with an error that
points at `endless worktree land`.

- **Pro**: collapses the two paths into one — there is no "non-land merge"
  to handle.
- **Con**: aggressive; locks users out of standard git workflow; only
  enforceable for sessions running under endless-hook.

### F. Surface uncommitted events as a warning, not a fix

Add a status check (`endless status --check-events` or a pre-commit hook)
that warns when events are dirty. Don't auto-commit; just shout.

- **Pro**: zero behavior change; informational only.
- **Con**: depends on user noticing the warning; the bug-prone manual step
  remains.

## Open questions for the events-log session

1. Which option (or combination) fits the project's clone-completeness
   contract best?
2. Is there a reason `endless worktree land` is the canonical merge path
   that I'm missing? (If yes, option E becomes more attractive — make the
   canonical path the only path.)
3. How should this interact with the multi-session concurrency model? Each
   session writes to its own `events-{node}-{seq}.jsonl`, so file-level
   conflicts between sessions are unlikely, but the auto-commit step is a
   shared write to git's index.
4. Backward compatibility: there are already existing dirty event files in
   developer checkouts (this session left some). Migration story?

## Out of scope

- Implementation of the chosen option. This task captures the decision; a
  follow-up implementation task should be filed once the design is settled.
- Changing the existing `endless worktree land` auto-commit behavior. It
  works; new design should be additive.
