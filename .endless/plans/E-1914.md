# Implementation spec — `session list` rework + per-session task hiding

Design is settled (E-1912). Do not re-open the decisions below; implement them.
Two threads, done in sequence in one session. Thread B is the more valuable half
— do it first if you have to choose where the care goes.

Relevant code found during design:

- `src/endless/session_cmd.py` — `session list` rendering (~line 648 builds the
  select; ~581–595 handles the existing hidden-*session* filter), and
  `hide_sessions` / `unhide_sessions` at ~841/855.
- `internal/events/session_tasks.go` — writes `session_tasks
  (session_id, task_id, relation_id, created_at, updated_at)`.
- `internal/sessionstatuscmd/` — `session status` / `--monitor` rendering, incl.
  `tree.go` and the existing icon vocabulary.

---

## Thread A — `session list` output

Target render:

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

1. **Drop the Summary column.** The discussion summary is not restored behind a
   flag — it is removed.
2. **Add an active-task-ID column**, and the task title in Summary's place.
3. **State renders as a single-width icon** with a legend printed below the
   table. This is the fix for the ragged output (`needs_input` is 11 chars and
   currently shoves every following column right). Reuse `session status`'s
   existing glyph vocabulary where it already expresses the same concept; only
   invent glyphs for states it has no equivalent for. The four states are
   `working | idle | needs_input | ended`.
4. **Project scoping.**
   - Default: current project only (detected from cwd, same detection the rest
     of the CLI uses).
   - `--all-projects`: all projects.
   - `--project <name>`: one named project, usable from anywhere.
   - `--project` + `--all-projects` together is an error, not a precedence rule.
   - The existing `--project TEXT` filter flag is the natural home for the third
     bullet — keep the name, change the default behavior around it.
5. **Project column appears only when the output spans >1 project.** For
   single-project output, name the project in the header line
   (`Sessions (20 of 550) — project: endless`) and omit the column.
6. `--json` is unaffected in shape except for the new task-id field; do not
   apply the icon substitution to JSON.

Existing `--hidden` / `--all` / `--empty` / `--state` / `--sort` / `--limit`
flags keep their current meaning. Note `--hidden` refers to hidden **sessions**
and is unrelated to Thread B.

---

## Thread B — per-session task hiding

### Storage

Add a nullable `hidden_at` timestamp column to `session_tasks`. Non-null means
hidden. Timestamp rather than boolean so `--only-hidden` can order by how long
something has been suppressed. Migration required.

Hidden-ness belongs to the *(session, task)* pair and must never be read or
written as a property of the task alone.

### Commands

```
session hide   [<session>] --task <task-id>...
session unhide [<session>] --task <task-id>...
```

- Optional positional session value; omitted = current session. Resolve it with
  the same resolver the other `session` subcommands use.
- `--task` is repeatable.
- **Without `--task`, both commands keep their existing session-level behavior
  verbatim.** `--task` is the discriminator; do not change the no-flag path.
- Hiding an already-hidden task is a no-op, not an error. Same for unhide.

### `session status` / `session monitor`

- **Default**: hidden tasks omitted, plus a footer indicator that is always
  printed when the count is non-zero: `… 2 hidden (--show-hidden)`. This is
  required — a hidden task must never disappear without a trace.
- `--show-hidden`: render everything, hidden rows visually distinguished (dim,
  or a distinct glyph — match the existing rendering idiom).
- `--only-hidden`: render the listing over the hidden set only. This is the
  discovery path for unhiding without knowing IDs.
- `--show-hidden` and `--only-hidden` together is an error.
- `--json` rows carry the hidden state.

`session monitor` inherits all three behaviors — the footer must survive the
redraw loop.

### Lifecycle

**A hide never expires on its own.** No status transition un-hides a task,
`unverified` included. Only explicit `session unhide --task` reverses it.

When a task reaches terminal status it drops off the list anyway and its hide
row goes inert; GC those rows or leave them, implementer's call.

### Non-goals — do not build these

- No effect on `task next`, blocking relations, landing, or any other session's
  view. Hiding is display-scoped.
- **Nothing that reasons about whether a session is finished** — no
  archivability computation, no exit-time warnings, no gating. "Archiving" is
  not an Endless command; it is the human exiting Claude and closing the tmux
  window.
- No hidden-count column on `session list` (considered and dropped).
- No unhide-by-ordinal (deferred).

---

## Verification

**Write `tests/tasks/e-1914-verify.sh`.** This task changes behavior across a
schema migration, a new flag surface, and a rendering invariant, so it takes a
verification script like the other 145 in `tests/tasks/`. Follow the shape of
`tests/tasks/e-1648-verify.sh`: pass/fail per check then a summary, exit 0 on
all-passed / 1 on any failure / 2 on a setup problem, and full isolation — a
throwaway git repo as project root under a temp dir with its own
`XDG_CONFIG_HOME` and `XDG_CACHE_HOME`, touching no real DB, ledger, or cache.
Exercise the freshly-built worktree binary, not the global install.

Checks it must cover:

- The `hidden_at` migration applies to an existing `session_tasks` table and is
  idempotent on re-run.
- With a task touched by ≥2 sessions: hiding it in one removes it there and
  leaves the other session's list **untouched**. This is the core per-session
  guarantee — a global-flag regression would show up here and nowhere else.
- The footer count is present and correct whenever anything is hidden, and
  absent when nothing is.
- `--only-hidden` finds the hidden task; `--show-hidden` renders it marked;
  `session unhide --task` restores it.
- `--show-hidden` with `--only-hidden` errors; `--project` with
  `--all-projects` errors.
- `session hide`/`unhide` with no `--task` still perform their existing
  session-level behavior unchanged.
- Hiding an already-hidden task is a no-op, not an error (same for unhide).
- Column alignment: a `needs_input` row no longer skews the table — assert
  against rendered column offsets, not eyeballing.
- The Project column is absent for single-project output and present when the
  result spans more than one project.

Alongside the script: Python tests via `just test`. The existing
`tests/test_session_*.py` family is the place to extend —
`test_session_status_add.py` and `test_session_order.py` are the closest models
for status-render assertions.
