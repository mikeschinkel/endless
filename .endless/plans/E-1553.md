# Decide transitive block computation at read time across blocked_by chains and parent_id

## 1. Scope

Decide whether `blocked_by` blocking semantics in endless propagate transitively, and through what dimensions. Two dimensions of transitivity exist:

- **Chain transitivity**: A `blocked_by` B `blocked_by` C — is A also blocked by C?
- **Parent transitivity**: A `blocked_by` B, and A has children A1, A2, ... — are A's children also blocked by B?

Today's behavior: blocking is stored explicitly as task-to-task `blocked_by` links. Neither chain nor parent transitivity is computed; only the direct link is consulted.

## 2. Why this matters now

Mike (2026-06-10): the tmux status bar now shows blockers for the current session's task. The at-a-glance information is valuable. But there's a real gap: if a parent epic is blocked, the user can still start any of its children without seeing the block — the children aren't currently considered blocked. That's a footgun. A blocked parent should imply blocked children, at least at the "can I start this?" level.

Read-side queries that should respect transitive blocks:

- tmux status bar (blocker display for current session's task)
- `endless task next` (filtering)
- `endless task claim <id>` (gate or warning)
- Web dashboard child trees

## 3. Options

### Option A: Pure non-transitive (status quo)
Source of truth: explicit `blocked_by` links only. Read-side queries use only direct links.

- **Pro**: simplest data model; no cycle-detection logic; explicit is honest.
- **Con**: footgun on parent-blocker case (Mike's stated concern); manual link maintenance to keep dependency chains accurate.

### Option B: Pure transitive (stored)
Source of truth: explicit links AND computed transitive blocks stored as additional rows.

- **Pro**: read-side queries are simple — just look up the rows.
- **Con**: write-time complexity (compute on every status change); easy to drift; doubles the storage; complicates "why is X blocked?" debugging because half the rows are derived.

### Option C: Hybrid — explicit storage, computed read-side (the compromise)
Source of truth: explicit `blocked_by` links only. Read-side queries that care about transitive blocking compute it on the fly via SQL CTE (recursive) or in-code traversal.

- **Pro**: storage stays explicit (one truth, debuggable); read-side queries get correct answers without manual link maintenance; per-query opt-in — some queries can use direct only, others compute transitive.
- **Con**: every read-side caller that wants transitive must implement (or call) the computation; performance characteristics differ per query.

## 4. Recommendation (to be confirmed)

**Option C.** The storage-vs-display split aligns with the database-vs-rendering split endless already uses, and explicit storage stays debuggable. The read-side computation is bounded — task trees and blocker chains in endless are not deep — so SQL CTE or in-code traversal is cheap.

Concrete near-term scope:

1. Define a Go helper `IsTransitivelyBlocked(taskID) (bool, []TaskID)` that returns whether a task is blocked considering both chain and parent transitivity, and the list of effective blockers.
2. Wire it into:
   - tmux status-line render (show transitive blockers for current task)
   - `endless task claim` (warn / gate when claiming a transitively-blocked task)
   - `endless task next` (filter out transitively-blocked tasks)
3. Leave write-path semantics unchanged — `endless task block` still creates explicit links only.

## 5. Out of scope

- Cycle detection on `blocked_by` chains (already needed regardless of this decision; file separately if not handled).
- Whether parent transitivity should be opt-in per task (e.g., a `parent_blocks_children` flag) — start with always-on, add the knob only if a case emerges.
- UI presentation of transitive blockers (color-coding, depth indication, etc.) — design at implementation time.

## 6. Inputs

- Mike's 2026-06-10 framing: tmux status bar use case; "don't let me start any child of a blocked epic."
- Existing `blocked_by` link table and current consumers.
- `endless guide` blocking semantics section (which currently says "B in verify → A is still blocked" etc. — needs updating once this lands).

## 7. Deliverable

One decision record (`endless decision add`) capturing the chosen option and the recommended near-term scope. Implementation tasks filed afterward.

## ## Justification

Research-deliverable task; the answer is a decision about endless's blocking semantics that affects multiple read-side consumers. Filing standalone rather than under E-1537 because the decision is endless-wide, not epic-specific.
