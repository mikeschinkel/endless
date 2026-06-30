# session_tasks.relation — classify scope-entry, incl. read-capture (gate) + display tiering

## Foundation (existing scope)
- Add `relation` to `session_tasks`: `goal` (claimed/active), `surfaced` (created
  during the session), `revisited` (pre-existing, edited/touched). Set at capture
  time. Feeds the You/Agent split and the Goal line in `session next` (rename pending:
  status/monitor).
- Per the analysis: `session_tasks` records ANY touch (incl. incidental grooming);
  the `relation` column + the render-side terminal-status filter (E-1421) separate
  goal work from incidental edits. Principle: **capture broadly, classify with
  `relation`, separate at display.**

## Extension — read-capture as a reliable gate (folded in 2026-06-29/30 with Mike)
Motive: `session next` is Mike's dashboard of "tasks related to this session's main
task." A task the agent READS during the session is, arguably, related — but relying
on the agent to remember an explicit "add" verb is a guideline that gets skipped.
Make read-capture structural (gates, not guidelines).

### Gate — auto-capture on read
- `task show` / `task detail` (the read path) auto-records the task against the
  current session under a NEW relation `referenced` (read-only relevance; lighter
  than `revisited`, which implies an edit). The CLI does it, so the agent can't
  forget and Mike never has to remember to remind.
- **Scope: AGENT reads only (Mike, 2026-06-30, current lean).** Rationale: a human
  who wants a task on the list can run `session task add`; the unreliable part is
  getting the agent to remember, so auto-capture targets agent reads. This makes
  agent-vs-human detection a PREREQUISITE — it resolves E-1462's open actor-identity
  question toward "must detect agent" (e.g. the CLAUDECODE env var Claude Code
  injects). The detection mechanism + role vocabulary remain open.

### Storage — `db-ledger/local`, not the shared WAL
- The `referenced` capture stays event-sourced (honors ED-1352, events-as-primitive
  for session-attributed activity) but its event goes to **`db-ledger/local`**
  (gitignored, machine-user), NOT the committed multi-dev ledger: reads are
  session-local, ephemeral, and per-developer, so they don't belong in shared
  history. Confirm against the current `session_tasks` event write path (session-
  scoped rows are already a live side-effect, not replayed by `rebuild-db`, per
  E-1683).

### Display — tier by relation, don't flood
- The flood risk (~10 context-reads in a session) is a DISPLAY problem, not a
  capture problem. Tier relations: `goal`/`queued` prominent, `surfaced`/`revisited`
  mid, `referenced` dimmed or collapsed — ranked by recency + read-count. A one-off
  glance sits at the bottom; a task read 3x and discussed floats up.

### Hide / remove — correct false positives (two distinct actions)
- `session task hide <id>` / `session task unhide <id>` — suppress from the DISPLAY
  but KEEP the association (mirrors the existing `session hide`/`unhide` for
  sessions). For a captured read that's relevant-but-noisy.
- `session task remove <id>` — drop the session association entirely. For a capture
  that was simply wrong. (Use `remove`, NOT `forget`: `session forget` already means
  "unset the shell's session env vars"; and NOT `delete`: nothing about the task
  entity is deleted, only its link to this session.)

### Explicit promote (the belt, optional)
- `session task add <ids>` (NOT `session add` — it adds a TASK to the session, not a
  session) promotes a task to `queued` = decided session work. Secondary to the
  read-gate, for intentional queueing.

## New relation values
- `referenced` — read-only relevance (auto, on AGENT read, stored local).
- `queued` — explicitly decided session work, not yet started (via `session task add`).

## Open questions / dependencies
- **Agent-vs-human detection** is now a prerequisite (read-gate captures agent reads
  only). Resolve the detection mechanism (CLAUDECODE env var?) + role vocabulary
  alongside the analysis's actor-identity question; the policy (agent-only) is
  decided, the mechanism is not.
- Confirm `db-ledger/local` supports session-scoped capture events on the write path.
- Migration sequenced against E-1459 (V-migration replacement) per the description.
- May split the verb / display-tiering work into child tasks if the plan grows.
