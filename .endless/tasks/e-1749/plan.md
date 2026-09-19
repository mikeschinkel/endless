## Goal

The `endless guide` happy-path still claims landing removes the worktree. Since E-1272 replaced land's destructive behavior, `endless worktree land` **retains** the worktree (and its branch) for the reaper to collect after a grace period. The stale text misleads sessions into believing a landed worktree is gone (it caused exactly that confusion in practice).

## Change

In `docs/guide/index.md`, the happy-path sentence (currently, ~line 37):

> When implementation is verified, land the work with `endless worktree land <id>` (auto-commits endless-managed files, rebases onto main, fast-forwards, removes the worktree).

Reword the parenthetical to match current behavior: the worktree and task branch are **retained** after land and reaped automatically after a grace period — not removed immediately. Also verify the "auto-commits endless-managed files" clause against current behavior: E-1272's title includes "drop auto-commit step," so confirm whether land still auto-commits before keeping that clause.

## Notes

- Anchor by the phrase, not the line number (the file has a generated cross-reference section below that shifts line numbers).
- Only `docs/guide/index.md` is canonical. `docs/private/agent-view-bundle/guide-index.md` carries the same phrase but is a git-excluded, derived bundle — do not hand-edit; it regenerates.
- Confirm the actual land behavior in the `worktree land` implementation (worktree_cmd) before finalizing wording — describe what the code does, not what an old docstring said.

## Verify

- `endless guide` no longer states that land removes the worktree.
- The wording matches actual `endless worktree land` behavior (retain worktree + branch; reaper collects later).
