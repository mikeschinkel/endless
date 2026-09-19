You are working on **E-1327** (Decide session-status data model: snapshot+slots vs event-stream+composition).

**This is a `decision`-type task.** Deliverable is a written design conclusion with rationale, captured in the task's text and outcome — not code. Subsequent implementation tasks will reference this decision as their architectural premise.

**Constraint: this decision precedes E-1319.** The read CLI (`endless session status list/show/latest`) cannot meaningfully be designed until the data model is defined. Don't let E-1319 unblock without an answer here.

## What surfaced the question

The current `session_statuses` table treats each row as a point-in-time snapshot of a session's reported state. The docs (`endless guide sessions`, line "Latest row by `created_at` is the current status") promise snapshot semantics. But in actual usage:

- Agents (Claude sessions) record DELTAS — what just changed in the recent turns — because re-stating everything every time is token-expensive and bog-slow. The latest row never contains the full session view.
- Readers expect snapshots and don't get them. The docs lie.
- Multi-phase sessions get flattened: only the most-recent phase shows; earlier arcs disappear from "current state."

So the model and the behavior are misaligned.

## Inputs to load and absorb

You must read all of these before forming a recommendation:

### 1. session_statuses.id=7 (this conversation's session, ES-389)

```
endless sql "SELECT * FROM session_statuses WHERE id = 7"
```

Read the row in full. Note especially:
- `headline`, `tasks`, `decisions`, `summary`, `notes` describe a moment in this session — NOT the full session
- The session also created E-1318 / E-1322 / E-1323 / E-1324 / E-1325 / E-1326 / E-1327 (this task) but those task creations are scattered across multiple session_status rows (id=4, id=5, id=7) — no single row holds them all

### 2. session_statuses.id=6 (session ES-356's input, with its analysis)

```
endless sql "SELECT * FROM session_statuses WHERE id = 6"
endless sql "SELECT analysis FROM tasks WHERE id = 1325"
```

The `tasks.analysis` field on E-1325 contains a 5-gap structural critique authored by ES-356 after recording its first real session_status row. Specifically:
- **Gap #1:** test-probe tasks have no place to be marked ephemeral
- **Gap #2:** cross-session handoffs have no slot
- **Gap #3:** runbooks (multi-step procedures) get buried in `<notes>`
- **Gap #4:** multi-phase sessions are flattened by latest-row-wins (this is the snapshot-model failure mode)
- **Gap #5:** empty sections clutter render

Plus two renderer wins (#6: auto-join task titles to IDs; #7: `<to-file>` slot for observation-to-task conversion).

### 3. The in-flight tasks that depend on this decision

```
endless task show E-1318    # narrative-column JSON conversion
endless task show E-1319    # read CLI (BLOCKED on this decision)
endless task show E-1322    # session_status_tasks filing table + auto-capture
endless task show E-1323    # smarter dedup for retry-rewording
endless task show E-1324    # CLI hang fix
endless task show E-1325    # the structural-gaps task (E-1325's direction is "snapshot+slots")
```

E-1322 is partially aligned with the event-stream direction (it adds an auto-captured per-aspect table for task filings). E-1325 is aligned with the snapshot+slots direction (more sections in the parent row). They reflect two different answers to the question this task decides.

### 4. The conversation history that produced this task

This decision task was created at the tail end of a long ES-389 conversation. The exchanges leading up to its filing covered:

- The dogfood-reflex problem (5 misses in one session) and why event-stream auto-capture would lower the recording cost
- Mike's filing-vs-activity distinction (filings idempotent; activity is time-series)
- The retry-rewording dup-row incident that led to E-1323
- The CLI hang that led to E-1324
- The observation that the docs and behavior are misaligned (no row is a true snapshot)

Read the latest few session_statuses rows (id=4, id=5, id=7) for the trail.

## The actual decision

Three named options:

**(A) Snapshot+slots.** Keep the per-row snapshot model. Add structured slots for what's currently missing: `<test-probes>`, `<handoff>`, `<runbook>`, `<to-file>`. Improve the renderer (suppress empty sections, auto-join task titles). Each row is still meant to be a complete snapshot of session state at a moment. The dedup model stays per-row.

Pros: incremental; rows remain self-contained; the read model stays "fetch the latest row." Cons: the underlying token-cost / re-statement problem is unsolved; agents will keep recording deltas, and the snapshot promise will keep breaking.

**(B) Event-stream + composition.** Each aspect (tasks, decisions, commits, memory, summary, notes, headline, runbooks, handoffs) becomes its own table. Some are auto-captured as side effects of existing events (E-1322 is the first slice for task filings). Some are appended by `endless session status add` calls. There is no parent "row" — there's a session, and there's a stream of typed events. "Current state" is composed by JOINs/aggregations in the read CLI.

Pros: matches how agents actually record (deltas append); auto-capture reduces dogfood-reflex cost; rich queryability; no flattening of multi-phase sessions. Cons: bigger schema lift; reads become aggregations not row fetches; the "snapshot as a thing" goes away.

**(C) Hybrid.** Keep `session_statuses` rows for *narrative* aspects that are intrinsically per-recording (headline, summary, notes, decisions). Add per-aspect child tables for *cumulative* aspects (tasks, commits, memory, handoffs, runbooks). Reads compose: "latest narrative + cumulative items since session start" or similar.

Pros: pragmatic; preserves familiar row-fetch reads for narrative; aligns event-stream where it adds value. Cons: two models to maintain; "snapshot vs event" decision pushed down to per-aspect basis.

## 1. Get oriented

```
endless guide
endless guide sessions
endless task show E-1327 --text
endless task claim E-1327
```

Then read all the inputs above before entering plan mode.

## 2. Do the work

Enter plan mode. Engage with Mike on the three options. The decision should also produce:

- An explicit list of which existing tasks (E-1318, E-1319, E-1322, E-1325) are unchanged, rescoped, or made obsolete by the chosen direction
- A pointer to whatever "first slice" implementation tasks should follow this decision (E-1322 is already filed; the chosen direction may add more)
- A doc-update task to fix `docs/guide/sessions.md` once the model is decided (the current "latest row = current status" line is incorrect under any of A/B/C)
- A separate observation (file as a small task or note in this decision's text): agents currently can't read `tasks.analysis` without `endless sql` — that's a gap in the task-show CLI surface that surfaced while building E-1327's input

## 3. Closing

When the decision is settled:
- `endless task update E-1327 --text /path/to/decision.md` with the full design write-up
- `endless task update E-1327 --status completed --outcome "<one-line summary of the chosen direction + key rationale>"`
  (E-1327 is type=decision; "completed" is the right terminal status for findings-as-deliverable)
- File any new follow-up tasks per the decision's recommendations
- Block / unblock dependent tasks accordingly (E-1319 should unblock when this lands)

Don't run `endless worktree land` — there's no code to land. Don't mark `confirmed` — Mike does that.

Goal: a written architectural decision that resolves the snapshot-vs-deltas mismatch, names the implementation order, and makes E-1319 (read CLI) actually plannable.
