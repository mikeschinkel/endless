# E-987 — Resolve event-log auto-append vs clean-main tension

> Part of E-968 (parallel-session support). Research task; deliverable is this design text.
>
> **Status**: resolved 2026-05-11. Implementation handed off to follow-up tasks (see below).

## The original conflict

Two prior designs collided when E-987 was originally filed:

1. **E-808** made the event log authoritative. Every endless CLI mutation appended to `.endless/events/events-*.jsonl` (renamed by E-1197 to `.endless/db-ledger/db-entries-*.jsonl`). The ledger is committed and replayable.
2. **E-971** declared `main` the sacred clean integration target. `endless worktree land` refuses dirty `main`.

Per E-972, all writes route to `main`'s `.endless/` directory regardless of which worktree the session is in. So every CLI invocation left `main` dirty, and without resolution every `land` call would refuse.

The v1 design (locked 2026-04-30) chose **auto-commit-at-land**: `land` auto-committed any dirt matching an `AUTO_COMMIT_GLOBS` set, as a separate `Endless: auto-record session activity` commit, before rebasing the worktree branch onto `main`. It also documented a recovery procedure for the rare case where the rebase conflicted on an auto-managed file — the documented procedure was `git rebase --abort && git checkout main -- <auto-paths> && retry land`.

## What the addendum surfaced (2026-05-11)

E-1240 (Add completed status gated by verbs.json completable flag) demonstrated a case the v1 recovery procedure mishandled. E-1240's feature commit legitimately added new entries and a new schema field to `.endless/verbs.json`. `endless worktree land E-1240` hit a rebase conflict because `main` had auto-recorded a separate verb addition (`bind`). The stock recovery hint would have told the user to `git checkout main -- .endless/verbs.json` — which would have silently destroyed E-1240's intentional schema additions. The conflict was resolved by hand.

The addendum opened four design questions; this section records their resolution.

## Architectural correction (the resolution)

The deeper issue the addendum surfaced is that **auto-commit-at-land is the wrong steady-state mechanism**. The v1 plan absorbed dirt on `main` to keep `land` working. That absorbs bugs: errant search-and-replace, agent error, user edits the user did not realize had been left behind. Those should never be silently committed.

Correct framing:

1. **`main` should never be dirty in steady state.** Once endless is hardened, neither endless nor Claude should leave `main` dirty. Dirt is a symptom, not normal state.
2. **`land` should NOT auto-commit anything except snapshots.** Db-ledger and verbs.jsonl writes get committed at the *point of write*, not at land. Snapshots stay auto-committed at land because they're posterity (write-once historical record), not transactional state.
3. **Auto-managed files are tiered by sacredness, not uniformly prohibited:**
   - `.endless/db-ledger/*.jsonl` — **sacred**. Hand-edits only as a last-resort merge-conflict resolution.
   - `.endless/plans/snapshots/*` — **mildly sacred**. Captured history; hand-edit not a crisis.
   - `.endless/verbs.jsonl` — **not sacred**. Actively maintained; hand-edits on `main` (if committed) are fine.
4. **The current recovery hint at `worktree_cmd.py:744–759` is actively unsafe** and must be replaced. The replacement is a "investigate this rebase conflict" message that names the conflicted paths and points at E-972's routing invariant — no destructive copy-paste recipe.

## Successor tasks (where the work goes)

Already filed and pre-existing:

| Task   | Status         | What it does for the resolution                                                                                |
|--------|----------------|----------------------------------------------------------------------------------------------------------------|
| E-1197 | verify         | Renamed `.endless/events/` → `.endless/db-ledger/`. Path-rename predicate for everything else here.            |
| E-1268 | confirmed      | Converted `verbs.json` → `verbs.jsonl` with `merge=union` in `.gitattributes`. Closes the E-1240 textual conflict scenario. |
| E-1206 | needs_plan     | Auto-commit each `.endless/db-ledger/` write immediately. Removes db-ledger from `land`'s auto-commit scope.   |
| E-1208 | needs_plan     | Auto-commit `verbs.jsonl` immediately on `endless verb add`. Removes verbs.jsonl from `land`'s auto-commit scope. |
| E-1216 | needs_plan     | Move plan-file writes from `main` to the active task worktree. Plan files stop being a `main`-dirt source.      |
| E-1207 | needs_plan     | Validate plan-file path against the task's worktree on `task update --text`. Enforcement layer for E-1216.     |
| E-1202 | needs_plan     | Forbid plan files at `.endless/plans/E-NNN.md` in `main`'s working tree. Hardens E-1216.                       |
| E-1061 | later, needs_plan | Generic post-edit commit prompt for any CLI mutating auto-committed files. Backstop for the broader pattern.|

Filed as part of this resolution (see "Resolution actions" below):

- **Bundled `land` change** — narrow `land`'s auto-commit to snapshots only; replace the destructive recovery hint with a non-destructive investigate message. Blocked on E-1206 and E-1208 so it doesn't ship before write-time auto-commit is in place.
- **merge=union shipping** — ensure `verbs.jsonl merge=union` in `.gitattributes` ships with v1.0 and design an in-Go merge fallback for projects that can't take the gitattributes change.
- **E-1017 rename** — update E-1017's decision text to use `db-ledger/` instead of the old `events/`.

## Resolution of the four addendum questions

### Q1 — Detection of feature-intent vs auto-record on rebase conflict

The partitioned-by-file-class detection with commit-subject probing was the wrong shape. E-1268's `merge=union` closed the headline E-1240 case. E-972's routing rule means db-ledger and snapshots can't legitimately have worktree-side commits. The broader shift (auto-commit at write time) removes the substrate for these conflicts altogether.

**Decision:** decline the partitioned-detection scope. Replace the recovery hint with a single "investigate" message naming the conflicted paths and the E-972 invariant.

### Q2 — db-ledger / snapshot conflict action

**Decision:** refuse with "routing bug — investigate." Per E-972 these shouldn't occur from a worktree side; auto-discarding worktree-side content silently destroys the only evidence of where routing broke. The replacement hint will name this directly.

This does not contradict the operational reality that snapshots and verbs.jsonl *can* be hand-edited on `main` when committed. The "routing bug" framing applies specifically to **worktree-side commits** on db-ledger / snapshots, which contradict E-972.

### Q3 — Scope split

**Decision:** decline E-1019 (it had been chartered for the v1 recovery procedure; that scope is no longer right). File the bundled `land` change as a new task under E-968.

### Q4 — E-1017 hand-edit prohibition wording

**Decision:** no textual carve-out for feature commits. The "hand-edit" wording covers manual edits, and Mike's operational tiering — db-ledger sacred, snapshots mildly sacred, verbs.jsonl not sacred — is operational guidance rather than a wording change. E-1017's text continues to apply to manual `vim` edits; programmatic commits from a worktree branch were never going to be the path for legitimate feature work anyway.

E-1017's text still uses the old `events/` directory name; that's filed as a standalone rename follow-up.

## Resolution actions taken on this task

1. This text replaces the v1 plan body and the 2026-05-11 addendum.
2. E-1019 transitioned to `declined` with reason recorded.
3. Follow-up tasks filed: (a) E-1017 events→db-ledger rename, (b) bundled `land` narrowing + non-destructive hint, (c) merge=union shipping / in-Go fallback.
4. This task transitions to `completed` (or `assumed` if the gate rejects).

## Critical files (for the follow-up tasks, not this one)

- `src/endless/worktree_cmd.py:53–58` — `AUTO_COMMIT_GLOBS`. The bundled follow-up reduces this to snapshots-only.
- `src/endless/worktree_cmd.py:702–730` — current auto-commit step at `land`. The bundled follow-up narrows it.
- `src/endless/worktree_cmd.py:744–759` — current destructive recovery hint. The bundled follow-up replaces it.
- `.gitattributes` — `merge=union` entry for `verbs.jsonl`. The merge=union shipping task ensures it ships with v1.0 and designs the in-Go fallback.
- E-1017's decision text — the standalone rename task updates it.

## What is explicitly NOT in scope going forward

- Per-file commit-subject probing on rebase conflict (was Q1's candidate detection signal). Wrong shape after the architectural shift.
- Auto-discard of worktree-side content for db-ledger / snapshots. Per Q2, refuse and surface.
- Carve-outs in E-1017's hand-edit prohibition text. Wording is fine; operational tiering lives in this document and the follow-up tasks.
- Auto-commit-at-land for db-ledger / verbs.jsonl. Those move to write-time per E-1206 / E-1208.
