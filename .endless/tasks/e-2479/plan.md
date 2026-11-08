# Plan

The gate's "code changed since the last reconciliation" refusal becomes a full account: the commit the last reconciliation was made at, the current HEAD, the files whose content differs, and the recovery as ordered commands with the actor on each. The recovery is two steps: the session runs `task reconcile E-N`, the user runs `task verify E-N`.

That two-step recovery exists because `task reconcile --file` is accepted at `unverified` when, and only when, the last reconciliation's fingerprint differs from the current one. It records the new reconciliation and leaves the status alone. Plan items stay frozen. A reconciliation that files a discrepancy says so and names the question, since verify will refuse until it is answered.

One Go function builds the recovery text; the gate, reconcile's refusal at a frozen status, and the branch-moved "unverified again" notice all use it. The reconcile context names up front the items that need `answer_holds` or `reask`.

Answered in chat on 2026-11-08: Q1 files plus the two commits (C); Q2 reconcile accepted at `unverified` when stale (B).
