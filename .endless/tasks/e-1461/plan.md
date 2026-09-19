# endless session next — live per-session what's-next view

## Goal
A compact, glanceable view of "what should I do next in THIS session," driven 100% from the DB (+ tmux/env) — never from agent judgment at render time. Lives in a short top-right tmux pane (above a notes pane and a CLI pane, beside a full-height Claude pane), refreshed live like `top`. One-shot CLI first (`endless session next`), then a `--watch` / top-like mode.

## Hard constraint: DB-derived, not AI-judged
Every cell is a stored column or a join over {sessions, session_tasks, task_deps, tasks}. The agent's job is to keep the *inputs* current (status transitions, `task block` edges, lineage capture) — not to compute rows. Two states the DB exposes that prose never did: **STALE** (in-process, no live session) and **IN FLIGHT** (another live session on the task).

## Row set (union; each row tagged by source)
- **touched**: `session_tasks WHERE session_id = me` (E-1462 relation classifies: goal / surfaced / revisited)
- **in-flow**: `subtree(me.active_epic_id)` via `parent_id`
- **elsewhere**: `tasks t WHERE ∃ live session s≠me with s.active_task_id = t` (cross-session IN FLIGHT)

flow + elsewhere are NOT `session_tasks` rows — they are unioned at query time. E-1462's column only classifies the touched slice.

## Next-Action (action-enum primary; You/Agent derived secondary)
First match wins, per (task t, my session m):
- `⟳ IN FLIGHT`  ∃ live session s≠m with s.active_task_id=t  → carry s.short_id + s.process (pane)
- `● ACTIVE`     t = m.active_task_id
- `▶ SPAWN`      canon = ready ∧ text IS NOT NULL
- `✎ PLAN`       canon = unplanned
- `☑ VERIFY`     canon = unverified
- `⚠ STALE`      canon = underway ∧ ¬∃ live session on t
- `? DECIDE`     canon = revisit
- `· LATER`      phase = later (overlay; sinks the row)

Actor tag (secondary): **You** = SPAWN/PLAN/VERIFY (a bare command you type); **Agent** = ACTIVE/STALE (this session's in-progress work). Sort: enum rank, then `tasks.sort_order`. Leverage proxy: `COUNT(task_deps WHERE source=t, dep_type='blocks')` = "blocks N".

## Status canonicalization (in-query; ship now, expose drift)
Canonical statuses: unplanned, ready, underway, unverified, confirmed, assumed, blocked, revisit, declined, obsolete. Non-canonical slugs seen in data (needs_plan, in_progress) are aliased in-query:
- unplanned, needs_plan → PLAN
- ready → SPAWN
- underway, in_progress → working (→ ACTIVE or STALE)
- unverified → VERIFY
- blocked → WAIT (manual)
- revisit → DECIDE
- confirmed / assumed / declined / obsolete → terminal (hidden, counted)

Duplicate slugs are real in-process-with-blocker tasks, not noise. Canon collapses them; the view makes staleness visible, which is the incentive to keep status true.

## Blocker = decoration, not a rival action
`blocked(t) := ∃ task_deps blocks→t with blocker.status ∉ terminal`. Render `⛒ E-NNNN (status)` on ANY row (including ACTIVE / IN FLIGHT) so in-process-and-blocked tasks — the population that is hard to land — stay visible. The `⏸ WAIT` *action* is reserved for would-be-SPAWN/PLAN tasks blocked from proceeding.

## Header (compact, ≤2 lines, top-style)
- Line 1: session short_id · FROM lineage · BLOCKS impact · ⟨reserved placeholder⟩ · action counts · uptime
- Line 2: focal epic + title · live-session count · freshness

Fields:
- **FROM** (`⤴ parent session / its epic`): needs **E-1128** — capture parent_session_id + spawning context at spawn, **write-once to the local `sessions` row** (NOT a committed event; sessions are machine-local, the WAL is shared across devs). Reads `active_epic_id` (durable), not `active_task_id`. Blocks E-1465.
- **BLOCKS** (`⛒ N tasks · M sessions`): free today — `COUNT(task_deps source=my.active_task)` + join to live `sessions.active_task_id`.
- **⟨placeholder⟩**: reserved for a TBD header field Mike will specify.

## Rendering
- **v1 = 1-line per task** (top-style): action · ID · canon-state · ⛒blocker / blocks-N · truncated title in a fixed trailing column · SRC. Maximizes rows in a short pane. (2-line record with the title on its own indented line = a later follow-up Mike files if needed.)
- **Alignment via manual width pass** (scan rows for max column widths) so a long title can never widen the action / ID columns; the title clips. `text/tabwriter` cannot interleave the title line without breaking global alignment.
- **Body caps to pane height**: top-N by rank, then an overflow tally (`+K more · ✎x ⏸y`).
- No Go table lib today (only stdlib `tabwriter`, once, in `internal/sandboxcmd/list.go`). v1 uses manual-width / tabwriter. The `--watch` / TUI adopts bubbletea + lipgloss (E-1622).

## Staging
1. **E-1465** one-shot `endless session next` — proves the CASE against the real DB.
2. **--watch / top-like** live refresh — E-1622 (bubbletea / lipgloss); the loop re-runs the same query. True value is live: IN FLIGHT / WAIT-clear / STALE all change as other sessions move.

## Constellation
- **E-1461** (this epic) — design home
- **E-1462** — `session_tasks.relation` column (goal / surfaced / revisited); blocked by E-1459 (migration-framework rewrite)
- **E-1465** — the read command (action-enum primary; + cross-session IN FLIGHT; blocked by E-1462 and E-1128)
- **E-1128** — capture spawn lineage (parent session + context), write-once to local `sessions` row; FROM-line prereq; blocks E-1465
- **E-859 / E-1622** — live TUI home for `--watch`
- **E-1125** — lineage introspection CLI (later)
- **E-1483** — `goal_task_id`: LIKELY OBSOLETE (durable goal is now the epic / `active_epic_id`); do not depend on it; Mike to revisit for salvageable ideas

## Maintenance contract (inputs the agent keeps true)
Status transitions; `endless task block` → `task_deps('blocks')` edges when a gate is found (not re-derived live); spawn lineage captured at spawn (E-1128). The view surfaces drift (STALE rows, things stuck in unplanned, missing blockers) — which is the reason to maintain the fields.

## Decisions (2026-06-28)
- DB-derived only; no render-time agent judgment. Retracted from earlier prose: leverage / root-cause ranking, file-overlap sequencing, runtime gate-checks → replaced by blocks-count, `sort_order`, stored blocks edges.
- Action-enum primary; You/Agent derived secondary.
- Cross-session IN FLIGHT in scope (extends E-1465).
- Blocker = decoration; STALE = in-process-without-live-session.
- 1-line rows for v1; manual-width alignment.
- Lineage = write-once local column captured at spawn (NOT a committed `session.spawned` event — sessions are machine-local; the committed WAL is shared across developers).
- `goal_task_id` (E-1483) dropped; anchor on epic / `active_epic_id`.

## Reserved
⟨placeholder — Mike has a further header field in mind; capture when specified⟩
