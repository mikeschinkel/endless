# E-1912 outcome — `session list` rework + per-session task hiding

Design settled with Mike, 2026-08-06. Implementation filed as a single task
(see "Follow-up" at the end). No code written under E-1912.

## Findings that shaped the design

Two facts from the code changed the shape of the answer:

1. **`session hide` / `session unhide` already exist** and operate on *sessions*
   (`sessions.hidden`; the existing `--hidden` / `--all` filters on
   `session list` read that column). This is not a collision: `--task` is the
   discriminator. `session hide 982` hides session 982; `session hide --task
   E-1234` hides that task from the current session. The two readings compose —
   `session hide 982 --task E-1234` means "in session 982, hide E-1234" — which
   supplies cross-session targeting with no additional flag.

2. **The session↔task association is already a table**:
   `session_tasks (session_id, task_id, relation_id, created_at, updated_at)`.
   Per-session hiding is one nullable `hidden_at` column on that table. No new
   table, no materialization work.

Live output confirmed the two `session list` complaints: `needs_input` (11 ch)
against `working`/`idle` is what ragged the columns, and `Summary` consumed
~90 columns to render text like "I'll start by getting oriented."

## Decision 1 — hiding is per-session, never global

Hidden-ness is a property of the *(session, task)* pair. `session status` lists
tasks a session **touched**, and touch does not imply dependency; the same task
routinely appears in several sessions' lists. The user decides, per session,
which of those he actually intends to handle there and hides the rest.

A global per-task flag would be actively wrong — it would hide the task from the
one session that is supposed to deal with it.

## Decision 2 — auto-unhide: never

No status transition un-hides a task, `unverified` included. A hide is a
standing statement that the task is not this session's concern; re-surfacing it
reintroduces exactly the noise hiding exists to remove. Only an explicit
`session unhide --task <id>` reverses it.

Housekeeping consequence: when a task reaches terminal status it leaves the list
anyway and its hide row goes inert. Implementation decides whether to GC those
rows or leave them; either is acceptable.

## Decision 3 — command surface for hiding

```
session hide   [<session>] --task <task-id>...     # hide task(s) from a session's list
session unhide [<session>] --task <task-id>...     # reverse it
```

- Session argument is optional and positional; omitted means the current session.
- `--task` is repeatable, so several tasks can be hidden in one invocation.
- Without `--task`, both commands retain their existing session-level behavior
  unchanged.

On `session status` and `session monitor`:

- **Default render**: hidden tasks omitted, with a footer indicator that is not
  optional — `… 2 hidden (--show-hidden)`. A hidden task must never vanish
  silently.
- `--show-hidden`: render everything, hidden rows visually marked (dimmed or a
  distinct glyph).
- `--only-hidden`: render the listing over the hidden set alone. This is the
  discover-and-unhide path, so the user never needs to have remembered an ID.

`--json` output carries the hidden state per row so machine consumers can tell
the difference.

Deferred, not rejected: unhide-by-ordinal from the last `--only-hidden` render.
Worth revisiting only if typing task IDs proves annoying in practice.

## Decision 4 — `session list` output

Summary column removed outright. The task is the session's identity now, so the
task ID and title carry it.

```
Sessions (20 of 550) — project: endless
ID    ◆  Task     Msgs  Title
────  ─  ──────   ────  ──────────────────────────────────────────
982   ▶  E-1596    206  Fix worktree bootstrap fallback
976   ▶  E-1874    336  Reap stale sandboxes on land
963   ?  E-1902    133  Session store audit
551   ‖  E-1479    126  Plan file mirror correctness

▶ working   ‖ idle   ? needs input   ␥ ended
```

- **State becomes a single-width icon** with a legend below the table. This is
  the fix for the ragged columns — a fixed-width glyph cannot push the
  following columns out of alignment the way `needs_input` did. Glyph choice
  should reuse `session status`'s existing vocabulary where one already exists
  for the same concept rather than inventing a parallel set.
- **Active task ID is a first-class column.**
- **Project scoping**: default to the current project only. `--all-projects`
  widens. `--project <name>` selects a specific project from outside its
  directory or when not in a session. `--project` and `--all-projects` are
  mutually exclusive (error, not silent precedence).
- **The Project column appears only when output spans more than one project.**
  Single-project output names the project in the header line instead, as above.

No hidden-count column on `session list` — considered and dropped; it did not
earn its width.

## Non-goals

- Hiding is display-scoped. It must not affect `task next`, blocking relations,
  landing, or any other session's view.
- **Nothing reasons about whether a session is finished.** No archivability
  computation, no warnings at session exit, no gating. "Archiving" is not an
  Endless command — it means the human exits Claude and closes the tmux window.
  Endless surfaces the list; the human decides when they are done with it.

## Follow-up

One implementation task, both threads in sequence — see the task filed with
`--cleans-up E-1912`.
