# Epic task type — plan

This is the strategic plan for adopting `epic` as a task type in endless. Implementation-level decisions for each child task get their own plans; this plan governs the shape (what an epic is, its lifecycle, its CLI surface) and points to the children that build it.

## 1. What an epic is

An epic is a **narrative initiative whose children emerge through planning**, where the epic-level plan is expected to evolve as work proceeds.

The epic's **deliverable is planning + coordination**, not code. Children deliver the code; the epic delivers the strategy that decomposed into those children, the running plan that captures discoveries from each child, and the coordination of dependencies between them. Operationally, an epic always has someone driving it — a coordinator session (see §6.5).

Two non-epic shapes (filed as adjacent concepts but explicitly out of scope here):

- **Checklist / sweep / batch / suite** — decomposition by enumeration; child list IS the plan; finishing one child teaches nothing about the next. E-1486-shape work is on the edge between epic and checklist; brainstorm settled on "epic" because each subsystem requires real per-subsystem design and the strategy can evolve.
- **Task with children** — a normal task that incidentally has sub-tasks (verification matrices, bug-fix follow-ups). The parent is still a deliverable.

## 2. Type as the representation

- `epic` is a new value in `tasks.type`. Added via the task-type-formalization root (see §8).
- No new table for epics (yet). If `epics` becomes its own table later, it's a clean migration; nothing in v1 precludes it.
- Verb requirement on title: keep (start with a registered verb). Evolve later if limiting.

## 3. Terminal-status semantics by type

| Type        | Terminal-shaped status            | Reopenable |
|-------------|-----------------------------------|------------|
| `task`      | `assumed`, `confirmed`            | yes        |
| `bug`       | `assumed`, `confirmed`            | yes        |
| `research`  | `completed`                       | yes        |
| `epic`      | `completed`                       | yes        |
| `<all>`     | `obsolete`, `declined`            | yes        |

Notes:
- All statuses are reopenable in principle (no hard-terminal). A confirmed task that surfaces a same-day bug can be reopened rather than spawning a new ticket.
- `completed` is gated by type. Tasks/bugs cannot use `completed`. Research uses the verb-`completable` path (ED-1502). Epics use a type-based path: type=epic ⇒ may-`completed`, no verb gate.
- `verify` does NOT apply to epics. Epics never go through user-testable verification; they go directly `in_progress` → `completed` when children are all handled.

## 4. Status meaning shift for epics

| Status         | Meaning on a task                | Meaning on an epic                                                                  |
|----------------|----------------------------------|--------------------------------------------------------------------------------------|
| `needs_plan`   | No design work yet               | No high-level strategy yet                                                          |
| `ready`        | Planned, ready to implement      | High-level strategy is set; ready to begin planning children                        |
| `in_progress`  | A session is working on it       | Any child is `in_progress` (auto-derived; see below)                                |
| `verify`       | Implementation done, awaiting verification | Not used on epics                                                          |
| `confirmed`    | Verified                         | Not used on epics                                                                   |
| `assumed`      | Believed done, deferred verification | Not used on epics                                                               |
| `completed`    | Not used on tasks/bugs           | All children are terminal (`completed`/`confirmed`/`assumed`/`declined`/`obsolete`) |
| `revisit`      | Was partially planned, needs re-evaluation | Strategy under reconsideration — load-bearing affordance for epics       |
| `declined`     | Active decision not to do        | Same                                                                                |
| `obsolete`     | Made irrelevant by other changes | Same                                                                                |
| `blocked`      | Waiting on something else        | Same                                                                                |

For v1, epic status is **auto-derived as a pure function of children's statuses**, evaluated top-to-bottom:

1. Any child has `status = in_progress` → epic `in_progress`
2. No `in_progress` but any child has `status = ready` → epic `ready`
3. No `in_progress` or `ready` but any child has `status = needs_plan` → epic `needs_plan`
4. All children are terminal (`completed`/`confirmed`/`assumed`/`declined`/`obsolete`) AND at least one child exists → epic `completed`
5. Epic has zero children → status unchanged (no derivation fires)

**Sticky-override statuses** block all derivation: `revisit`, `declined`, `obsolete`, `blocked`. When the epic is in one of these, derivation reads the state and does nothing. The override must be explicitly cleared (set the epic to a derivable status) before derivation resumes. Manual status sets on an epic accept ONLY these four values; other values are rejected by the write-path validator (per E-1543) since derivation handles them.

Reopenability consequence: adding a `needs_plan` child to a `completed` epic makes the epic `needs_plan` again per rule 3. Adding a `ready` child makes it `ready`. Adding an `in_progress` child makes it `in_progress`. No special case needed.

**`verify` for epics is filed as a maybe (E-1535)** — an optional retrospective gate before `completed`, adopt if the gap is felt.

**Claim and status are independent.** A coordinator session claiming an epic (§6.5) doesn't override auto-derived status. Claim binds a session, creates the worktree, and gives the tmux discoverability; status continues to reflect child-state per the rules above. A claimed epic can still be in `ready` (no child started yet), `in_progress` (a child started), or `revisit` (coordinator set it explicitly).

## 5. Revisit semantics on epics

`revisit` on an epic signals "the strategy is being reconsidered." Children continue executing under the current strategy until each session prompts its user to decide.

**Session-level pause-on-revisit hook** (v1): when a session whose claimed task is a descendant of a `revisit`-state epic next invokes a tool, a PreToolUse-style hook fires, blocks, and prompts the user: *"Your parent epic E-NNN has been set to revisit. Continue or pause until the strategy is re-set?"* The session's operator has the in-flight context to decide — the epic-flipper does not.

Mechanism = hook (NOT `endless channel` — the existing inter-session messaging path is too constrained and is explicitly off the table for this affordance, per Mike 2026-06-05). The hook reads the claimed task, walks up the ancestry chain to find epic(s), checks status. Matches the existing pattern endless uses for the worktree-enforcement and commit-on-main gates.

## 6. CLI surface

Parallel to `endless decision`:

- `endless task add --type epic …` — primary form; what AI sessions will use.
- `endless epic add …` — alias for human typing speed; same effect.
- `endless epic show <id>` — defaults to including `--children`. Add `--no-children` for scripting.
- `endless epic list` — alias for `endless task list --type epic`.
- `endless epic update <id> …` — alias for `endless task update <id> --type epic`.

**Guide update**: surface `endless epic` (and `endless decision`) as the AI-facing preferred form. The current guide's `task --type <X>` examples are the reason AI sessions reach for that pattern; this is a self-citation problem (I wrote that guide). Belongs as a sub-task of the Epic epic.

**`endless task next` behavior on epics**: leave as-is for v1. If epics surfacing in `next` feels noisy, add filtering later.

## 6.5 Epic management semantics

Epics are claimed via `endless task claim` like other tasks. Claim creates the per-epic worktree, binds the session, and provides the tmux window and discoverability needed to drive the epic operationally. The claimed session is the **coordinator**: it doesn't do code implementation (that happens in child sessions), but it owns:

- filing children as the plan decomposes them
- updating the epic-text plan as discoveries surface (`endless task update <id> --text ...`)
- watching child-status (auto-derived) and reacting — filing follow-ups, setting `revisit` if strategy needs reconsideration
- coordinating dependencies across children

Coordinator sessions are typically long-lived but can hand off via `task release` → another session `task claim`. Multiple coordinator handoffs over an epic's life are normal.

Claim binds a session but does not override auto-derived status (§4). A claimed epic can still be `ready`, `in_progress`, or `revisit` based on child state — claim sets up the worktree, not the status.

## 7. Promotion (task → epic)

`endless task update <id> --type epic` flips type. Validations to run at promotion time:

- **Status check**: if current status is `verify` (task-only) → block with prompt to set a different status first.
- **Verb check**: skipped. Per §3, type=epic implies may-`completed` without a verb gate.
- **Children check**: existing children remain attached; semantics shift to "children of an epic."
- **Plan**: existing `tasks.text` is task-grained; user/AI is expected to revise it to epic-grain. Not enforced.

No DB-side issue. Filtering code that assumes type=task needs an audit (quick grep) — most queries already pass `--type` explicitly or don't filter on it.

## 8. Adjacent work this depends on

- **Task-type formalization root** (still to file): owns the `task_types` FK table (per memory `feedback-no-db-check-constraints`), schema.sql seeding with `INSERT OR IGNORE`, app-level validator, and cleanup of currently-miscategorized records (8 `plan`, 16 `chore`, 49 `bug`). Adding `epic` and formalizing `research` happen here. Epic-type can land before cleanup completes; cleanup is data debt the gate exposes.
- **ED-1502** (already recorded): `completable` verb-gate for type=research `completed` status.
- **E-1531** (filed): `task_content` table for typed content. Until E-1531 lands, research `--justification` text goes in `tasks.notes` under a `## Justification` heading. After E-1531, justification moves to a `task_content` row of `type=justification`.
- **E-1532** (filed, later): formalize phases/statuses with same FK-to-values-table pattern. Sibling concern; epic type doesn't block on it.

## 9. Research-type formalization (folded in for completeness)

Goes in the task-type formalization root, but listed here because this plan depends on it.

- `type=research` becomes an authorized type with `completed` as its terminal status.
- Creation gate: `--type research` accepts without `--justification` if `--parent <id>` is type=epic AND status=in_progress; otherwise requires `--justification "<why this can't be inline research in a do-task>"`.
- Justification storage: `tasks.notes` under `## Justification` heading until E-1531 lands.

## 10. Lane / epic_id (E-1421 overlap)

Lanes and epics are orthogonal but may associate. `lanes.epic_id` (optional FK) is the lightweight integration. Belongs in the E-1421 conversation post-epic-landing, not in the Epic epic itself.

## 11. Deferred (intentionally not in v1)

- Narrative as typed `task_content` (waits on E-1531)
- Web dashboard epic views (CLI matures first)
- `endless task next` filtering of epics
- `verify` status on epics (filed as maybe — E-1535)

`posture=open` / extensibility field was considered and **decided against** during design (2026-06-04). Epics are by-nature reopenable; `completed` (vs. `confirmed`) captures that without a separate field. Listed here only to mark it as a closed question.

## 12. Open questions

None known. If any of the above surfaces a disagreement on review, that's the right time to settle it.

## 13. Children of the Epic epic

Filed children. Each gets its own implementation plan at pickup.

- E-1540 — Implement `endless epic` CLI aliases (add/show/list/update)
- E-1541 — Implement auto-derivation of epic status from child states
- E-1542 — Implement pause-on-revisit hook for sessions on epic-descendant tasks
- E-1543 — Implement promotion validation for `task update --type epic`
- E-1544 — Implement research creation gate with parent-epic exemption and --justification requirement
- E-1545 — Document epic semantics in `endless guide`
- E-1546 — Update `endless guide` to surface noun-aliased verbs as AI-facing preferred form

Adding the `epic` row to `task_types` is NOT a separate child — it ships in E-1538's seed values. The originally-filed E-1539 ("Add epic value to task_types") was marked obsolete on 2026-06-10 (see E-1539 outcome).
