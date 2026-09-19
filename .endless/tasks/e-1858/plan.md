# Remove the deprecated verbs.json glob from the auto-managed lists

## Problem
`.endless/verbs.json` is deprecated (superseded by `.endless/verbs.jsonl`); it is
untracked and no longer written. Two mirrored glob lists still name it:

- `src/endless/worktree_cmd.py` `AUTO_COMMIT_GLOBS` (line ~62):
  `".endless/verbs.json",  # legacy — still seen during E-1268 migration`
- `internal/monitor/worktree_anomalies.go` `AutoManagedStatusGlobs` (line ~40):
  same entry, but with **no comment** — an undocumented dead glob.

The two are required to stay in sync (per the doc comment on the Go list:
"Mirrors src/endless/worktree_cmd.py AUTO_COMMIT_GLOBS — keep the two in sync").

## Fix
If the E-1268 migration is complete (verbs.json no longer produced anywhere),
drop the `.endless/verbs.json` entry from BOTH mirrors. If it must linger, at
least add the same "legacy — E-1268 migration" explanatory comment to the Go
copy so the two agree and the entry isn't mistaken for a live path.

## Notes
- Harmless today (the glob matches nothing), so this is pure hygiene.
- The sibling entries `.endless/db-ledger/*.jsonl` and `.endless/verbs.jsonl` are
  live and must stay.

## Origin
Found auditing the worktree unsettled/dirty predicate (`taskWorktreeDirty`) and
its auto-managed filter during E-1834.
