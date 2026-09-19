# E-1648 — Add a submitted status and approve gate to the task status workflow

## Context / governing decisions
This implements an approval gate already decided in pieces:
- **D-1522** (accepted): `ready` = approved-to-spawn, plan optional — a task reaches `ready` via
  an approved plan OR an approved sufficient description.
- **D-1513** (proposed): plan-attach must not auto-promote; a pre-approval state exists (named
  `unapproved` there) before a human `approve` → `ready`.
- **D-1498** (accepted): today drafted-awaiting-approval is encoded as `unplanned` + text present
  (no separate status).
- **D-1499** (accepted): status changes go through the `endless task change` picker of valid
  transitions (no typed status value).

This task **supersedes D-1498's "reuse unplanned+text"** by introducing a distinct pre-approval
status, and **renames D-1513's `unapproved` → `submitted`** (positive/active; pairs with a
`submit` action). Net lifecycle:

    unplanned → submitted → (approve) ready → underway → unverified → confirmed | assumed

`submitted` = spec-complete, agent-set, awaiting approval; reachable two ways — the agent attached
a plan OR judged the description a sufficient spec (plan-vs-no-plan carried by whether `tasks.text`
is populated, not by status). `unplanned` = genuinely needs design. This makes unplanned-vs-submitted
the spawn-readiness signal the agent sets proactively (kills the recurring "is this enough to
spawn?" question).

## Decisions (resolved 2026-07-02 with Mike)
- Pre-approval state is named **`submitted`** (rename of D-1513's `unapproved`).
- **E-1648 owns defining the complete canonical status set** — no prior decision retired `blocked`.
- The workflow gets a **new consolidating decision** (not a finalize-in-place of D-1513).

## Deliverables
1. **Status + verbs.** Add `submitted`; add `submit` (agent-set) and `approve` (human → `ready`)
   verbs. Remove the plan-attach auto-promote (D-1498/D-1513). Forbid `kind=background` sessions
   from `approve` and from picking up non-`ready` work.
2. **Define + enforce the canonical status set.** E-1648 owns this. Working set:
   `unplanned, submitted, ready, underway, unverified, confirmed, assumed, revisit` + terminal
   `completed, obsolete, declined`. **Remove `blocked`** from every code path (e.g. the
   active-status `NOT IN (...)` filter in task_cmd.py) — blocking is carried by the `blocked_by`
   relation, not a status. Confirm `revisit` and the terminal set while sweeping.
3. **Teach every status classifier the set** — actionable / terminal / blocks-dependents / picker
   transitions (D-1499). Status literals live at ~two dozen sites across Go (sessionstatuscmd,
   monitor/task.go, events/executor.go, web/…) and Python (task_cmd.py, db.py, cli.py,
   suggestions_cmd.py); sweep them so none is blind to `submitted` or still treats `blocked` as live.
   **Specifically `classify`** (`internal/sessionstatuscmd/session_status.go`) must map `submitted`
   to a non-plan action (`actDo` or a distinct verb), NOT `actPlan` — else a planned-but-unapproved
   task keeps mis-showing as "needs a plan" in `session status`; add a `submitted` row to
   `session_status_test.go` (it already covers `unplanned`/`needs_plan`/`revisit`). See the
   self-demonstrating example below.
4. **Mermaid workflow chart.** A single `stateDiagram` of the full lifecycle, embedded in README.md,
   CLAUDE.md, and endless guide from one canonical source. Renders from the deliverable-5 decision.
5. **New consolidating decision** (`endless decision add`): "Canonical task status workflow" —
   references and supersedes D-1522, D-1513, D-1498, D-1499. Records the full valid status set
   (deliverable 2) and every legal transition. Single source of truth the mermaid renders from.

## Verification
- `endless task submit` / `approve` round-trip in a sandbox DB: unplanned → submit → submitted →
  approve → ready; a background session is rejected from `approve`.
- Grep confirms no classifier still treats a removed status (`blocked`) as live; the mermaid
  renders in all three docs from the single source.



## On completion (memory prune)
Once this ships, prune the converted agent-memory files feedback_plan_attach_promotes_to_ready.md and feedback_proactive_spawn_readiness.md + their MEMORY.md index lines — the lessons now live in the submitted/approve gate (per E-1723).



## Scope correction (E-1723, 2026-07-05) — decouple + de-conflict
Resolves a duplicate-ownership overlap with E-1532 and a `later`-blocks-`now` inversion.

- **Decoupled from E-1532** (no longer `blocked_by`). The approval gate ships on the
  CURRENT free-TEXT `tasks.status` column — adding `submitted` is a string value
  today; no values-table/FK is required. E-1532's FK formalization is independent
  (phase `later`) and will later seed the then-existing `submitted`.
- **Ownership moved to E-1532.** Deliverable 2 ("Define + enforce the canonical
  status set") and "remove `blocked` from every code path" now belong SOLELY to
  E-1532 (values tables + FK + 9-row cleanup + drop `blocked`). Deliverable 2 above
  is SUPERSEDED — E-1648 does NOT define the whole set or drop `blocked`.
- **E-1648 scope is now pure behavior:** add the `submitted` status value; `submit`
  (agent) + `approve` (Mike) verbs; remove plan-attach auto-promote; forbid
  `kind=background` from `approve` and non-`ready` pickup; teach classifiers about
  `submitted`; the mermaid lifecycle chart; the consolidating status-workflow
  decision. Deliverables 1, 3, 4, 5 stand; 2 is E-1532's.



## Self-demonstrating example (E-1723, 2026-07-05)
`endless session status` renders E-1648 itself as `✎ plan` ("needs a plan") even though it
has a full plan (`tasks.text` populated, ~5KB), because `classify`
(`internal/sessionstatuscmd/session_status.go`) maps status `unplanned` → `actPlan`
REGARDLESS of plan text (per ED-1522). E-1648 sits in `unplanned` only because it is
planned-but-unapproved and no `submitted` status exists yet — the exact two-state collapse
this task removes. Concrete motivating evidence, not a `session_status.go` bug. The fix is
scoped inline in Deliverable 3 above (`classify` must map `submitted` off `actPlan`).


