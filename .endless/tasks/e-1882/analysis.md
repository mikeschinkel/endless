## Corpus measured 2026-08-30 (E-2084)

The E-2084 survey classified all 86 unsettled worktrees by hand — exactly the
work this task automates. The result sizes each of the three buckets in the
description and supplies a ready-made test corpus.

**Bucket (a) — endless-managed metadata, safe to drop: 40 branches.** Their only
unlanded commits touch `.endless/` (ledger segments, plan mirrors). Landing them
replays bookkeeping and nothing else.

**Bucket (c) — real work, bail out and land: 10 branches.** E-1360, E-1523,
E-1643, E-1646, E-1653, E-1680, E-1697, E-1853, E-2010, E-2081.

A fourth case the description does not cover, and should:

**Superseded non-metadata content.** Four branches (E-1537, E-1785, E-1844,
E-1934) carry only commits `98bb7b24` / `4e611073`, a README rewrite E-1832 has
since superseded — main's README is a later rewrite. These are non-endless-managed
files, so rule (c) bails out and hands the operator "real work that needs land"
when landing them would *revert* main. E-1853's own two commits are the same
shape: they fix a verb gate E-1658 removed, and `src/endless/session_gates.py`
is gone from main.

Rule (c) is therefore too coarse. "Touches a non-endless file" is not the same
as "contains work main lacks". The cheap discriminator is `git cherry main
<branch>`: patch-id already tells you main has an equivalent commit, and it
collapses this corpus from 86 branches to 14 (see E-2087, which fixes the same
SHA-vs-patch-id assumption in `task unsettled`). What patch-id cannot tell you
is that main solved the problem a *different* way — that stays an operator
judgement, and the output should present it as one rather than deciding.

## A recurring dirty-file case worth special-casing

37 worktrees carry a byte-identical uncommitted patch to
`internal/monitor/db.go` (an "E-1972 compat sweep", 2026-08-26) that main has
since superseded with `homeRelative()`. 25 of them are unsettled for that reason
alone. Reconcile should recognise an uncommitted change whose effect main
already contains and offer to discard it — the manual answer here, as in the
E-1537 land that surfaced this task, was `reset --hard main`.
