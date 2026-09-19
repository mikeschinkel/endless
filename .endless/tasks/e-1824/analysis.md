Recurring problem surfaced by the E-1659 land. Long-lived worktrees drift behind
main (which advances constantly here). A stale worktree carries a stale binary,
so its hooks/CLI run old code against a moved-on main — producing schema/enum
skew (e.g. e-1755: a Jul-26 binary, 99 commits behind, no E-1818 gate, fail-
closing its UserPromptSubmit hook against the E-1659-migrated real DB), plus
missing features and stale verbs.jsonl. The enum error is one face of the class
"this worktree is out of date with main."

The manual remedy is a multi-step, footgun-laden dance (the one run for E-1659):
commit → merge/rebase onto main → recreate `.endless/worktree.json` (dropped by
rebase) → `go-work-init` → rebuild binaries. Each step has a known trap:
companion is untracked; `.endless/*.jsonl` is the append-only WAL (rebase can
reorder/duplicate it); merge-vs-rebase is unsettled; and the binary MUST be
rebuilt or the hooks stay stale. That tribal knowledge belongs in a gate, not a
checklist — hence a first-class `endless worktree sync`.

Name/placement (decided with Mike): `endless worktree sync`, not `session
rebase` — it operates on the worktree (branch + files + binaries), which Endless
already groups under `endless worktree`; nothing in the `sessions` table
changes. "sync" over "rebase" because it does more than a git rebase (companion,
go.work, rebuild), so an intent-name is more honest than the mechanism. A no-arg
form (current session's worktree) gives the session-style ergonomics.

Design crux: the git update runs over the append-only ledger WAL, so merge-vs-
rebase is a correctness question, not cosmetics — this ships the safe MERGE
default and leaves configurability to E-1109 (the existing `discuss` task for
merge-vs-rebase pref). E-1364 (land bundles verbs) is the push side of the same
staleness problem; sync is the pull side. Rebuild is delegated to a pluggable
project hook so the shipped verb never hardcodes `just`.
