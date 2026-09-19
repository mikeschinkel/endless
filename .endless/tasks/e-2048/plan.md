# E-2048 — review the self-dev isolation area, reduce it to a minimum set

## Why this exists

Three sessions in one day each filed against prior art they failed to find
(E-1729 missed twice, E-1959 missed once). The tasks in this area contradict
each other, overlap, and reuse one word for three unrelated mechanisms. Fixing
them one at a time has added tasks, not removed them.

Output is a reorganized ledger and a written question list. No code.

## Step 1 — name the axes

The word "route/routing" currently means at least three things, which is why
titles collide and why the same defect gets filed twice. Establish one term per
axis and require it in titles:

- **A. Which DATABASE a command reads and writes** — real vs per-worktree
  sandbox. E-1281, E-1737, E-1839.
- **B. Which FILES a command writes, where, and whether it commits them** —
  event ledger, decision and task companion files, doc copies. E-1729, E-1730,
  E-1665, E-2035.
- **C. Which BINARY runs** — main's build vs a worktree's. E-1972, and the two
  tasks folded into it (E-1704, E-2038). NOT this epic; coordinate only.

Deliverable: the vocabulary, plus retitles for every task using the banned word.
Known offender: E-1733's title. "Route" is banned as a verb per the user.

## Step 2 — rule on E-1733's outcome

E-1733 decided the target policy for ledger writes and their commit behavior. It
is landed and `completed`. The user now doubts the outcome is still valid, given
how much has been filed around and against it since.

Read the outcome, compare it against what actually shipped (E-1729's sandbox
ledger dir and commit skip) and against what is still open (E-2035). Rule:

- **Still valid** → cite it as the governing decision for this epic and file
  nothing.
- **Superseded** → `task replace E-1733 --by <this task>`. E-1733 stays
  `completed`; the relation records that its conclusion was overtaken rather
  than that it never happened.

Do NOT reopen it. It shipped, and its outcome is the historical record of what
was decided at the time.

## Step 3 — close what is dead

For each task in the area, decide: still needed, already done, or never needed.

- `obsolete` for never-needed and for made-irrelevant-by-other-changes.
- `task replace <old> --by <new>` where a newer task took over.
- Leave shipped work alone.

Known candidates:

- **E-1665** — the user believes it is done. Unconfirmed: it asks for a
  safe-by-construction primitive (`emit --dry-run` or a read-only verify mode)
  and no such flag exists in `internal/eventcmd`. Determine whether E-1729 or
  E-1962 removed the hazard by another route, or whether a hand-run
  `event emit --project-root <real-repo>` still appends before the guard
  rejects. Obsolete only if the hazard is gone.
- **E-1730** — excising already-leaked events. Confirm the leak stopped (it was
  blocked on E-1729, which landed) and re-measure how many rows remain; the
  count may now be zero, which closes it.
- **E-1839** — a brainstorm about the same confusion this review is resolving.
  Likely absorbed here; check before assuming.

## Step 4 — the open questions

Every question this review cannot answer without the user goes in a single
written list, each with the options and a recommendation. This is the primary
deliverable: the area is stalled on unmade decisions, not on unwritten code.

## Step 5 — the minimum set

Whatever survives stays parented under E-2050, each with a plan. Target is the
smallest number of tasks that still need doing, not a comprehensive inventory.

## Constraints

- Coordinate with E-1972 (axis C, `underway`); do not absorb it.
- The count of tasks in this area must go DOWN. A review that ends with more
  tasks than it started with has failed, whatever else it produced.



## Seeded open question (from E-1733's session, 2026-08-23)

**Settings/override isolation (axis B):** endless-managed skip-worktree files (the
E-998 `.claude/settings.json` worktree override) block any rebase that spans a
change to the committed copy ("could not detach HEAD"); recovery is clear-flag →
restore → rebase → regenerate. Should endless's rebase-performing operations
(land; resume/reopen recovery) do this automatically for files they can prove
endless generated (content matches generator output), stopping loudly otherwise?
Does the vocabulary require settings-writing tests to target sandbox fixtures —
which is what makes "differs from generated" a trustworthy human-intent signal?
And should settings.json support project-rooted paths rather than absolute or
home-rooted ones (Mike's suggestion), so a worktree override is portable and an
intentional edit could be promoted to main without path translation?



## Seeded findings (from E-1733's session, 2026-08-24)

- **Land convergence loses deterministically to any concurrent bulk writer.**
  E-1733's land failed 8/8 retries twice (2026-07-31 and 2026-08-24). Forensics
  on the second failure: one amend of main's rolling ledger commit per retry,
  all eight from one live session's bulk `task.fields_updated` re-triage
  (session 1124) — every amend rewrites the tip SHA and invalidates the
  just-computed fast-forward. E-2049 (write-lease; linked here) carries the full
  evidence in its analysis; rule on it as part of this area's commit machinery.
- **Land trusts the companion `.endless/worktree.json` `base_branch`, which
  nothing refreshes after a manual rebase.** During recovery its advice anchored
  to a months-stale SHA (`d6844526`) and BOTH of its suggested recovery paths
  would have rewound the branch to July. Land should recompute
  `git merge-base` instead of trusting the companion — or every
  rebase-performing operation must refresh the companion.
- **E-1733's residue is fully resolved** (context for Step 2's ruling): the
  outcome file reached main via the doc-mirror backfill; the two LESSONS.md
  commits were captured as hygiene commits direct to main (301687c9, 72e81797);
  the branch now contains nothing unlanded and the worktree is safe to drop.
  The "cannot land because nothing needs committing" clause formerly in this
  task's description was inaccurate and has been removed.


- **Land cannot settle a branch whose content is already fully upstream without
  hand-driven prep.** E-1733's final land (2026-08-24, landed on 58e43f47)
  succeeded only after a session manually rebased so git would drop the
  already-applied commits AND refreshed the companion base. Until then,
  `session status` showed the task unsettled with no automatable path to settle
  it — the user's proposed `worktree reset` need. Generalization to rule on:
  land should recognize the zero-delta case itself (drop already-upstream
  commits, recompute the base, settle worktree + session state) instead of
  erroring and leaving the settling to whoever remembers why.
