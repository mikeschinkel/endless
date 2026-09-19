Scope decision (with Mike, 2026-07-11): FULL — A1 + A2 + B, unified by one new
`session-query last-landing` Go query. A1 (branch merged) is git-only; A2 (branch
also reaped) and B (rewrite/descendant gate) both need the landing SHA, so they
share the query. B was weighed against E-1417's in-flight after-conflict diagnosis:
kept because it upgrades the amend case from a guessed candidate (after a confusing
conflict) to a precise refusal BEFORE the rebase. E-1417 stays the safety net for
source conflicts B can't predetermine. E-1417 has LANDED (main @ b1efcc28), so E-1308
branches from current main and reuses E-1417's shipped recovery wording (no
rebase-onto-in-flight).

Recovery framing (Mike, 2026-07-11): Facet B's refusal must NOT prescribe a BARE
`git reset --hard` — whoever runs `land` may not be the branch's author, so a blanket
destructive command risks losing commits. Resolved by reusing E-1417's SHIPPED
already-landed recovery (verified in main @ b1efcc28): it captures the branch delta to
a patch BEFORE the reset, so no work is lost even if followed blindly. B extracts that
block into a shared `_already_landed_recovery(wt, base)` and adds an ownership-confirm
note. (Earlier worry that E-1417 prescribed a bare reset was wrong — it already
candidate-frames with a "can duplicate or lose work" caveat and captures the delta
first.)

---

Added case (Claude session, 2026-07-09; now folded into the plan as Facet B):

The `git branch --merged main` test catches only FULLY-merged branches. It misses
the re-land failure where a branch's commit was `git commit --amend`-ed AFTER its
content had already been landed: the branch is a partial duplicate of main plus a
new delta — not fully merged — so `--merged` is false and land proceeds into a
source-file rebase conflict.

Gate (reuses task_landings, which records each landing's SHA): on a re-land (task
has >=1 task_landings row), require the branch HEAD to be a descendant of the last
landed SHA — `git merge-base --is-ancestor <last_landed_sha> HEAD`. A correct
follow-up (a new commit stacked on a branch first rebased onto main) passes; an
amend/rewrite fails. On failure, refuse BEFORE the rebase and print the reset+reapply
recovery rather than a misleading auto-file message.

Concrete repro: E-1750's first commit was landed; a follow-up was made by amending
that commit instead of stacking a new one; the next `just land` rebase re-applied the
whole change onto a main that already had it -> CONFLICT in
internal/sessionstatuscmd/session_status.go. Recovery was manual reset-to-main +
re-apply-delta. Couples to E-1417 (the diagnosis half).
