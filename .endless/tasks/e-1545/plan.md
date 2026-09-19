# E-1545 — Document epic semantics in endless guide

## 1. Scope

Add epic semantics to `endless guide` as a new section within `docs/guide/tasks.md`. No new top-level section file. Discoverable via `endless guide tasks`.

## 2. Why inline within tasks.md

Epics are a task type. Adding them to the existing tasks guide keeps the top-level section count small (the explicit project preference) and matches the conceptual relationship: epics are a flavor of task with extra rules. Decisions live in their own section because decisions are a separate noun entirely (different storage, different verbs, different lifecycle). Epics aren't — they share the task table, the task verbs, and the task display surface.

## 3. New section structure within tasks.md

Add a new top-level section "## Epics" near the end of `tasks.md`, after the existing relation-types content. Subsections within:

- **What an epic is** — narrative initiative; deliverable is planning + coordination; children deliver the code; contrast briefly with "task with children" (parent-still-a-deliverable) and checklist-style work (decomposition by enumeration).
- **Type and terminal status** — short table from E-1537 §3. State that `completed` is gated by type: epics get it for free, tasks/bugs do not, research gets it via the `completable` verb.
- **Status meanings on an epic** — condensed table from E-1537 §4 showing how each status reads on an epic.
- **Auto-derivation** — the pure-function-of-children rule (from updated §4): any in_progress → in_progress; any ready → ready; any needs_plan → needs_plan; all terminal → completed; sticky-override blocks all derivation.
- **Sticky-override statuses** — list `revisit`, `declined`, `obsolete`, `blocked`; explain they block auto-derivation.
- **Manual status sets on epics** — only sticky-override values accepted (per E-1543); other values rejected because derivation handles the rest.
- **Claim and status are independent** — claim binds a session and creates the worktree but does not override auto-derived status.
- **Coordinator pattern** — the claimed session is the coordinator: owns child filing, plan updates, watching child status, dependency wrangling; handoff via release+claim is normal.
- **Revisit semantics and the pause-on-revisit hook** — flipping an epic to `revisit` prompts each in-flight descendant session at next tool use to continue or pause.
- **CLI surface** — `endless task add --type epic` AND `endless epic add` as parallel forms; `endless epic show <id>` (defaults to including children); `endless epic list`; `endless epic update`.
- **Promotion** (`task update --type epic`) — the `verify` block; existing children carry over; the plan-grain shift is the author's responsibility.
- **Not used on epics** — `verify`, `confirmed`, `assumed` (one-line "why" each).

## 4. Content rules

- No `E-NNNN` or `ED-NNNN` references anywhere in shipped docs (per `feedback_no_internal_task_ids_in_shipped_docs`). Reference behaviors, not tickets.
- No em-dashes in new content.
- Tone matches existing `tasks.md` and `decisions.md`: direct, second-person to the agent, examples in fenced bash blocks, tables for enumerations.

## 5. Other edits

- **`docs/guide/index.md`**: add "epic" to the verb cross-reference table (auto-generated block — run `just guide-index` after edits). Add a topic row for "epic auto-derivation" → `section: tasks`.
- **`docs/guide/help/_topics.md`**: add topics "epic auto-derivation", "coordinator session", "pause-on-revisit hook", each pointing at `section: tasks`.
- **`docs/guide/tasks.md` line ~82**: update the `--type` comment to reflect the actual set (`task|bug|research|epic`). Add a `endless epic add` example as the preferred noun-aliased form.

## 6. Verification

- `endless guide --list` returns `decisions`, `orchestration`, `reference`, `sessions`, `tasks` (unchanged — no new section).
- `endless guide tasks` shows the new Epics section.
- `python -m endless.guide_map check` exits clean (no orphans, no bad sections, index in sync).
- `grep -E 'E-[0-9]{3,}|ED-[0-9]{3,}' docs/guide/tasks.md` returns nothing within the new Epics section.

## 7. Implementation sequence

1. Draft the new "## Epics" section within `docs/guide/tasks.md` (sub-sections per §3).
2. Update the `--type` example comment near line 82.
3. Update `docs/guide/help/_topics.md` with the three new topic rows.
4. Run `just guide-index` to regenerate the cross-reference table in `index.md`.
5. Run `just guide-check`. Eyeball `endless guide tasks` output.

## 8. Critical files

- `docs/guide/tasks.md`
- `docs/guide/help/_topics.md`
- `docs/guide/index.md` (auto-regenerated section only)
