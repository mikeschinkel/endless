# Plan step 4: does anything in Endless assume main is linear?

Asked: if local main gains a merge commit M (`git merge origin/main` after main and origin diverged), does anything break?

**Answer: no.** Nothing walks main with `--first-parent`, counts main's commits as a line, or rebases main itself. Every reader of main's history uses `base..HEAD` / `base...HEAD` ranges, `--no-merges` patch comparison (`--cherry-mark`, `range-diff`) or `--is-ancestor`, and all of those handle a merge commit correctly.

| Component | Effect of a merge commit on main | Verdict |
|---|---|---|
| land: `git rebase <base>` (worktree_cmd.py ~4704, rehearsal ~4223) | M and origin's commits are already in base, so they are never replayed; the branch lands on top of M | safe |
| land: `_drop_orphan_amendable_commits` (`log base..HEAD`, `rebase --onto`) | `base..HEAD` holds only the branch's commits either way | safe |
| land: `_ledger_touching_commits` | same range, M excluded | safe |
| land: `merge --ff-only <branch>` | branch was rebased onto M, so the fast-forward holds | safe |
| landgate `merge-base` + `ownCommits` (`--cherry-mark --no-merges base...HEAD`) | `--no-merges` drops only M; origin's commits arrive through M's second parent and count as base-side work, so their migrations are still seen | safe (degraded only for a migration created inside M's own conflict resolution) |
| ledger `canAmend` (events/commit.go) | amends only a tip whose subject is "Endless: record ledger entry"; M never matches, so the next ledger write appends on top of M | safe |
| unlanded cache: `unlandedRevsFrom`, `classifyBaseMovement` | `range-diff` ignores merges; the old tip is M's first parent, so base movement reads "appended" and the cache stays warm | safe |
| `task_landedness`, `sync_worktrees`, `land_conflict`, `LedgerOrphans` | ranges and `--is-ancestor` only | safe |

Ledger conflicts during the merge itself: db-ledger segments are per machine, and `verbs.jsonl`/`LESSONS.md` are `merge=union`, so a merge does not conflict on Endless's own files unless the same machine wrote ledger entries on both sides.

## Consequence for the divergence fault (plan 1d)

Merging origin into main is the cheap choice: it costs one merge commit. Rebasing main onto origin is the expensive one: every unpushed main commit gets a new SHA, so the land gate refuses every open task branch until each runs `git rebase main` (SourceBaseRewritten), the unlanded cache rebuilds from scratch, and the ledger amend loses its SHA-level sharing check (only E-1955's tree-hash backstop remains). The fault names both and recommends neither. It states each one's cost and leaves the choice to the user.
