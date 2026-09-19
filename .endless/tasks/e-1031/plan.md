# Session Focus

## Context

Mike keeps re-asking Claude variants of "where are we and what are we working on?" within the same session. The session DB tracks `active_task_id`, but a task is too narrow — it captures the work-unit, not the *agenda* of the conversation. When work goes down rabbit holes (which it routinely does), the AI loses track of how the rabbit hole connects back to the original purpose, and the human has to keep re-briefing it.

Concrete pattern (from a recent session, paraphrased):

> "E-1004 landed. Outstanding for E-971: E-985, E-986, E-1012, E-1019. What's next?"
> "Which do you recommend?"
> "E-1012, because [reasoning]. Want me to start?"

That whole exchange — table of in-progress work + recommendation + decision — should live as a *status snapshot under a focus*, not be regenerated from scratch each round. The focus is "Ship parallel sessions feature (E-968 cluster)." The status snapshot supersedes the prior one when work advances.

**Goal.** Add a Session Focus entity that:

- Captures session-level intent as committable prose, not just an active-task pointer.
- Forms a tree per session (primary at root, children for rabbit-hole detours).
- Gets injected into AI context every turn so "where are we?" doesn't need to be re-asked.
- Plugs into the E-968 W3 behavioral gate as a third clearance path (alongside `task start` and `task add`).
- Supports cross-session handoff for "we brainstormed a side issue here, spawn a session to work it."

**Out of scope.** LLM-based topic-drift detection (E-968 already declined this — deterministic gates only). Auto-generation of status snapshots without user-initiated triggers. Replacing tasks (focus references tasks via FK; tasks remain the work-unit primitive).

---

## Data model

### `focuses` table (DB projection — rebuilt by `rebuild-db`)

```sql
CREATE TABLE IF NOT EXISTS focuses (
    id INTEGER PRIMARY KEY,
    focus_id TEXT NOT NULL UNIQUE,            -- "EF-12"
    project_id INTEGER NOT NULL,
    session_id INTEGER NOT NULL,              -- FK sessions.id (the session that originated it)
    parent_id INTEGER,                        -- FK focuses.id; NULL for primary
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',    -- active|completed|abandoned|handoff|reopened
    prose_file_path TEXT NOT NULL,            -- '.endless/focus/EF-12.md'
    handoff_to_session_id INTEGER,            -- FK sessions.id when status='handoff'
    handoff_to_focus_id INTEGER,              -- FK focuses.id (the new session's primary)
    spawned_from_focus_id INTEGER,            -- reverse pointer (set on the spawned-into primary)
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    closed_at TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
    FOREIGN KEY (parent_id) REFERENCES focuses(id) ON DELETE SET NULL,
    FOREIGN KEY (handoff_to_session_id) REFERENCES sessions(id) ON DELETE SET NULL,
    FOREIGN KEY (handoff_to_focus_id) REFERENCES focuses(id) ON DELETE SET NULL,
    FOREIGN KEY (spawned_from_focus_id) REFERENCES focuses(id) ON DELETE SET NULL
);
```

### `sessions` table — additions

```sql
ALTER TABLE sessions ADD COLUMN primary_focus_id INTEGER
    REFERENCES focuses(id) ON DELETE SET NULL;
ALTER TABLE sessions ADD COLUMN current_focus_id INTEGER
    REFERENCES focuses(id) ON DELETE SET NULL;
ALTER TABLE sessions ADD COLUMN status TEXT NOT NULL DEFAULT 'active';
    -- active|completed|abandoned (manual close path; auto-flips to completed
    -- when last open focus in tree closes)
```

### `tasks` table — addition

```sql
ALTER TABLE tasks ADD COLUMN focus_id INTEGER
    REFERENCES focuses(id) ON DELETE SET NULL;
```

Set when a task is created or started while a focus is current. Doesn't migrate if the focus closes (a task records the focus it was created under, like a commit records its parent).

---

## Storage — files as prose truth, events as structure truth

Mirrors the tasks/plans split:

- **`.endless/focus/EF-NNN.md`** — committed to git. Source of truth for prose. YAML frontmatter mirrors the structural fields (for human/git readability and for `rebuild-db` recovery if events log is lost). Body holds Description + Status snapshots.
- **Event log** (`.endless/events/events-*.jsonl`) — source of truth for structural mutations: `focus.created`, `focus.updated`, `focus.closed`, `focus.handoff`, `focus.set_current`. `rebuild-db` projects these into the `focuses` table.
- **DB `focuses` table** — projection. Never written directly outside the event-log path.

### File format

```markdown
---
id: EF-12
project_id: 1
session_id: 233
parent_id: EF-10           # NULL/omitted for primary
status: active
title: Ship parallel sessions feature (E-968 cluster)
created_at: 2026-04-30T10:00:00Z
---

## Description

<initial agenda — set on creation, generally stable>

## Status — 2026-04-30T15:00:00Z

<table of in-progress tasks, what shipped, what's next, recommendation>

## Status — 2026-04-30T17:30:00Z

<superseding snapshot after the next round of work>
```

Latest `## Status` block is what `endless focus current` returns. Description is set once and rarely edited. New status snapshots append as new sections; old ones stay as in-file history (and git tracks the file).

---

## Lifecycle

### Creation at session start

`SessionStart` hook (in `cmd/endless-hook/claude.go`) checks: does the registered session have `primary_focus_id IS NULL`? If yes, inject a system reminder:

> No primary focus set for this session. Before tool use, ask the user what we're working on and run `endless focus add "<title>" --as-primary --text <path>`.

`UserPromptSubmit` hook re-injects the same reminder on every turn until `primary_focus_id` is set. **Non-blocking** — does not gate tool use. (You said nag, not block, in the original spec; matches that.) The user-facing burden is one extra question at the top of any session, which addresses the "I keep having to re-explain" problem at its root.

If the user refuses ("just exploring"), the AI captures *that* as the focus ("Orient self in subsystem X; no concrete deliverable yet"). Per #6 in our discussion: there is no such thing as a session without a focus.

### Currency — moving the pointer

`sessions.current_focus_id` is the column that tracks "where are we right now in the tree." Mutates via:

- `endless focus child "<title>"` — create new focus as child of current; set new one as current
- `endless focus pivot "<title>"` — create new focus as sibling under primary; set as current
- `endless focus close EF-N` — auto-walks current pointer to parent (or primary if parent gone)
- `endless focus goto EF-N` — explicit move within tree (rare; for revisiting a paused branch)

### Pivot detection — integrates with E-968 W3 gate

E-968 already designs:

- `kind='pivot'` matchers in `.endless/config.json` (case-insensitive: "actually", "wait", ...; case-sensitive: "PIVOT")
- `UserPromptSubmit` hook fires Layer 2 deterministic gate on match
- Clearance via CLI verb (`task start <id>` self-clears; `task add ...` draft-and-approve)

**Add a third clearance path: focus action.** When a pivot phrase fires the gate, AI can clear it by either:

- `endless focus child "<title>"` — "this rabbit hole is still under primary; track it as a child" (self-clearing, like `task start`)
- `endless focus pivot "<title>"` — "this is a real direction change; new sibling under primary" (draft-and-approve, like `task add`, since it's a higher-level shift)

Layer 1 (always-on context refresh) currently injects `Active task: E-XXX`. **Extend it** to also inject:

```
Active session focus
  Primary: <title> (EF-N)
    └─ <child title> (EF-M)
      └─ <child title> (EF-K)  ← current
Latest status: <copy of latest ## Status block from EF-K's prose file>
```

This is the breadcrumb that resolves the "where are we?" problem at its root. The hook reads `sessions.current_focus_id`, walks parents to primary, reads the latest `## Status` block from the current focus's prose file, formats, injects.

### Closing — terminal states

A focus closes via one of three terminal states:

- **`completed`** — work done in this session.
- **`abandoned`** — no longer relevant.
- **`handoff`** — spawned a new session to carry it forward; carries `handoff_to_session_id` and `handoff_to_focus_id`.

`endless focus reopen <id>` flips status back to `active` (with status field `reopened` for audit). Useful when a handed-off focus needs late additions ("forgot to capture this — it's relevant in session B").

### Handoff flow

User in session A, current focus EF-12 (a child of primary EF-10), decides to spawn a fresh session for EF-12's work:

1. `endless focus handoff EF-12` — prompts for the new session bootstrap.
2. Endless creates a new session record (and worktree, once E-968 W3 lands), generates a new `EF-NNN` as that session's primary, copies EF-12's prose into the new file's Description, sets `spawned_from_focus_id=EF-12` on the new primary.
3. EF-12's status flips to `handoff`; `handoff_to_focus_id` set.
4. AI in the original session continues with current pointer auto-walked to EF-12's parent (EF-10).

### Session completion

- **Auto:** when the last open focus in the session's tree closes, `sessions.status` flips to `completed`.
- **Manual:** `endless session close [--reason completed|abandoned] [--abandon-open | --handoff-open <session-id>]`. Refuses without an explicit flag if open focuses exist (per #6 spirit: silence is not a decision).

---

## CLI surface (agent-facing)

Per your "running endless commands in chat is bad UX unless AI runs them" — these are primarily for AI to invoke as gate-clearance and pointer-management actions, not for users to type.

```
endless focus current [--tree] [--json]
    Show current focus + breadcrumb (or full session tree).

endless focus add "<title>" [--as-primary | --parent EF-N] [--text <path>]
    Create a focus. Sets as current.

endless focus child "<title>" [--text <path>]
    Sugar: create child of current focus. Sets as current.

endless focus pivot "<title>" [--text <path>]
    Sugar: create sibling under primary. Sets as current. Higher-level shift than child.

endless focus update EF-N --status <path>
    Append a new ## Status block to the focus's prose file.
    The path is the markdown body to append (timestamp added automatically).

endless focus close EF-N --reason completed|abandoned
    Mark focus closed. Walks current pointer to parent.

endless focus handoff EF-N
    Close as handoff; emit details for spawning new session.

endless focus reopen EF-N
    Flip status to active (with audit trail of prior closure).

endless focus goto EF-N
    Move current pointer; rare, for revisiting paused branches.

endless session close [--reason ...] [--abandon-open | --handoff-open <sid>]
    Manual session close. Refuses without explicit flag if open focuses exist.
```

---

## Hook integration

Two existing hook points, augmented:

### `SessionStart`
- Existing: register session, worktree resolution (E-968 W3).
- **Add:** if `primary_focus_id IS NULL`, queue the "ask for focus" reminder for next `UserPromptSubmit`.

### `UserPromptSubmit`
- Existing (E-968 W3): Layer 1 active-task injection; Layer 2 pivot-phrase gate.
- **Add to Layer 1:** breadcrumb + latest `## Status` block of current focus.
- **Add to Layer 2 clearance options:** `endless focus child` (self-clears) and `endless focus pivot` (draft-and-approve).
- **Add nag:** if no primary focus set, inject the reminder block.

No new hook events needed.

---

## Critical files

**Schema:**
- `internal/schema/schema.sql` — add `focuses` table, `sessions` ALTERs, `tasks.focus_id` column.

**Event log + projection (Go):**
- `internal/monitor/focus.go` (new) — event types, projection logic into `focuses` table.
- `internal/monitor/db.go` — register migration; project `focus.*` events.
- `cmd/endless-event/main.go` — emit `focus.*` events from the file/CLI path.

**Files-as-truth (Python):**
- `src/endless/focus_cmd.py` (new) — CLI verbs (`focus add/child/pivot/update/close/handoff/reopen/goto/current`).
- `src/endless/cli.py` — register `focus` command group.
- `src/endless/focus.py` (new) — file IO for `.endless/focus/EF-NNN.md` (frontmatter parse/write, status snapshot append, breadcrumb traversal).
- `src/endless/task_cmd.py` — when a focus is current, set `focus_id` on tasks created via `task add`.
- `src/endless/session_cmd.py` — `session close` verb; query/display focus state.

**Hook integration (Go):**
- `cmd/endless-hook/claude.go`:
  - `handleSessionStart` — focus-missing nag scheduling.
  - `handleUserPromptSubmit` — extend Layer 1 with focus breadcrumb + latest status; extend Layer 2 clearance to recognize `endless focus child`/`endless focus pivot`.

**Existing patterns to reuse:**
- `.endless/plans/E-NNN.md` filesystem layout — copy verbatim for `.endless/focus/EF-NNN.md`.
- `tasks.plan_file_path` precedent — same shape for `focuses.prose_file_path`.
- E-968 W2 matchers config — `kind='pivot'` rows already exist there; no new matcher infrastructure needed.
- E-968 W3 Layer 1/Layer 2 hook code — extension points, not new hook plumbing.
- `rebuild-db` projection pattern — `focus.*` events project the same way `task.*` events do today.

---

## Verification

**Creation + nag.**
- Start a fresh session in this project. Verify `SessionStart` injects the focus-missing reminder. Verify it re-injects on each user turn until `endless focus add ... --as-primary` runs.
- Run `endless focus add "Test agenda" --as-primary --text /tmp/desc.md`. Verify `.endless/focus/EF-NNN.md` exists with frontmatter + Description; verify `focuses` row + `sessions.primary_focus_id` set.

**Layer 1 breadcrumb injection.**
- Create primary EF-1. Add child EF-2. Add child of EF-2 → EF-3 (current).
- Send any user message. Verify the system reminder injected by `UserPromptSubmit` shows the three-level breadcrumb and EF-3's latest `## Status` (or "no status snapshots yet" if Description-only).

**Status snapshot lifecycle.**
- `endless focus update EF-3 --status /tmp/snapshot1.md`. Verify a new `## Status — <ts>` block appended to the file.
- Run another `--status /tmp/snapshot2.md`. Verify it appends below the first; `endless focus current` shows the *latest* block, not both.
- Verify git diff on the focus file shows clean append (no rewrite of prior sections).

**Pivot gate integration (depends on E-968 W3 landing).**
- With E-968 W3 active, type "actually let's go look at the auth bug." Verify Layer 2 fires.
- Clear via `endless focus child "Investigate auth bug"`. Verify gate clears, EF-4 created as child of EF-3, current pointer moves to EF-4.
- Repeat with "PIVOT — switching to docs cleanup" → clear via `endless focus pivot ...`. Verify EF-5 created as sibling under primary EF-1, current pointer at EF-5.

**Handoff.**
- With current = EF-3, run `endless focus handoff EF-3`. Verify EF-3 status → `handoff`; new session created with new primary EF-N; EF-N has `spawned_from_focus_id=EF-3`; EF-3 has `handoff_to_focus_id=EF-N`.
- In the original session, verify current pointer auto-walked to EF-3's parent.
- `endless focus reopen EF-3`. Verify status → `active`.

**Task FK.**
- With EF-2 current, run `endless task add "Test work item"`. Verify the new task's `focus_id` column points at EF-2's row.
- With current = NULL (no focus), `endless task add ...` succeeds; `focus_id IS NULL`. (Tasks created outside any focus context.)

**Session completion.**
- Close all focuses in the tree (`endless focus close EF-N --reason completed` for each leaf, walking up). Verify `sessions.status` auto-flips to `completed` when the last open focus closes.
- Separately: with open focuses, run `endless session close --reason abandoned` — verify it refuses. Run `endless session close --reason abandoned --abandon-open` — verify all open focuses flip to `abandoned`, session status to `abandoned`.

**rebuild-db round-trip.**
- With several focus files present, run `rebuild-db`. Verify the `focuses` table is reconstructed correctly, `sessions.primary_focus_id` / `current_focus_id` re-resolve, `tasks.focus_id` re-links.

---

## ID naming convention

Focus uses the prefix **`EF-`** (Endless Focus). Tasks today use `E-` (which originally meant "Endless" but in practice has come to mean "task ID"). This plan establishes a forward-looking two-letter convention:

- `EF-NNN` — focus
- `ES-NNN` — session, *if/when* sessions get a typed string ID (today they remain integer + harness UUID; not introduced in this plan)
- `ET-NNN` — task, *eventually* (see follow-up below)

Existing `E-NNN` task IDs remain unchanged in this PR. The migration to `ET-NNN` is filed as a follow-up Endless task to be run in a quiet window with no parallel sessions in flight (atomic rename across plan files, event log, schema display fields, and prose cross-references).

**Follow-up to file as an Endless task before this plan ships:**

> Title: Migrate task IDs `E-NNN` → `ET-NNN` for two-letter prefix consistency.
>
> Description: Once Session Focus lands with `EF-NNN`, the codebase has two ID conventions in flight (`E-` for tasks, `EF-` for focus). This task migrates tasks to `ET-NNN` so all entity types share the `E<Type>-NNN` shape. Must run in a quiet window: pause all sessions, run migration script (rename `.endless/plans/E-NNN.md` → `ET-NNN.md`, rewrite event log task references via translation pass, update display fields in `schema.sql`, regenerate any cached cross-references), verify `rebuild-db` round-trips, restart sessions. Acceptance: `endless task list` shows `ET-NNN` IDs; old `E-NNN` references in plan prose are not auto-rewritten (preserved as historical text); new task creation uses `ET-NNN`.

Filing this as a real task — not just a note in chat — is the only way to keep the phased plan from calcifying into permanent inconsistency.

---

## Sequencing notes

This depends on E-968 W3 (the `UserPromptSubmit` hook + matchers config) for the pivot clearance integration. The data model and CLI surface are independent and can land first; the hook integration follows once E-968 W3 ships. If desired, focus could land in two phases:

1. **Phase 1**: schema + files-as-truth + CLI + `SessionStart` nag + Layer 1 breadcrumb injection (no Layer 2 dependency).
2. **Phase 2**: Layer 2 clearance integration once E-968 W3 lands.

Phase 1 alone solves the "where are we now?" problem (the breadcrumb is the high-leverage piece). Phase 2 adds the deterministic-pivot-handling polish.
