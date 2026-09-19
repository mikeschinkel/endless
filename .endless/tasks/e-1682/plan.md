# E-1682 — Durable session-navigation trail (manual + goto)

## Context

E-1681 shipped `session goto`/`back` with an intentionally ephemeral, goto-only
back-stack (a tmux server option). While designing it, Mike noted the navigation
*path* itself has durable value — usability analysis of how sessions get
navigated, and recovering a session you lost track of. This task adds a separate,
**durable** trail that captures *every* focus change (manual moves and `goto`),
decoupled from `session back` (which stays goto-only and predictable).

## Decisions (resolved with Mike — implement as written, not as open choices)

- **Single capture point.** A global tmux hook on focus-change events records
  every move. `session goto`'s `switch-client` is caught by the same hook, so
  goto carries no recording logic of its own.
- **Events hooked:** `client-session-changed` and `session-window-changed`
  (moving between Claude sessions == changing window/session). Do **not** hook
  `pane-focus-in` (intra-window noise).
- **`via` tag:** `session goto` sets a one-shot tmux server option
  `@endless_nav_via=goto` immediately before switching; the recorder reads + clears
  it, writing `via=goto`, else `via=manual`. This is the only change to landed
  E-1681 code.
- **Scope/keying:** per tmux **client** (the navigator), matching the back-stack
  model. Edge endpoints are stored as session ids (resolved from the pane) plus
  the raw panes for untracked locations.
- **Storage tier:** a new mutable runtime table in the local DB, written directly
  in Go — the **same non-committed tier as `sessions`**, NOT the committed JSONL
  ledger. High-churn nav state must never pollute the shared multi-dev ledger.
- **Retention:** unbounded append in v1 (single-user, unshipped). Pruning is
  deferred and explicitly not built now.
- **Read surface:** a minimal `endless session trail` viewer ships in v1.

## Data model

New migration under `internal/schema/changes/` (a real `.sql` file — tool-openable;
mirror an existing `changes/*.sql`). Follow the house schema conventions: no
`TIMESTAMP` type (TEXT + ISO), FKs as separate clauses, `_id`-suffixed integer FK
columns, and **no CHECK constraints** — the enum is an FK to a seeded values table.

`session_navigations`:
- `id INTEGER PRIMARY KEY`
- `client TEXT NOT NULL` — tmux `client_name` (the navigator)
- `project_id INTEGER` — FK `projects(id)`
- `from_session_id INTEGER` — FK `sessions(id)`, nullable (first move / untracked)
- `from_pane TEXT` — raw pane id when the source isn't a tracked session, nullable
- `to_session_id INTEGER` — FK `sessions(id)`, nullable (untracked destination)
- `to_pane TEXT NOT NULL` — the new focus pane
- `via_id INTEGER NOT NULL` — FK `nav_via_kinds(id)`
- `created_at TEXT NOT NULL` — ISO timestamp

`nav_via_kinds(id INTEGER PRIMARY KEY, slug TEXT)` seeded idempotently with
`(1,'manual'), (2,'goto')`. Go side: the int-backed enum pattern (explicit numeric
constants + `String()` + `ParseNavVia()`).

## Components (anchored by symbol — confirm exact sites at implement time)

1. **Schema migration** — `internal/schema/changes/e-1682-*.sql`, plus the seeded
   `nav_via_kinds` rows. Mirror the structure of a recent `changes/*.sql`.
2. **Recorder subcommand** — a new `endless-go` verb (mirror
   `internal/sessionquerycmd/` or `internal/tmuxcmd/`), e.g.
   `endless-go tmux record-nav --client <c> --pane <p>`:
   - resolve `--pane` → session id + project (reuse the pane→session helpers in
     `internal/monitor/tmux_lookup.go`, e.g. `GetLiveSessionByProcess`,
     `GetActiveTaskForPane`).
   - read this client's most-recent row → use its `to_*` as the new `from_*`.
   - read + clear `@endless_nav_via` to set `via`.
   - write the row via the **sessions runtime-write path** (mirror
     `internal/monitor/session.go` / `internal/events/executor.go`), not the
     committed ledger.
   - keep it fast and silent — it fires on every focus change.
3. **Hook install** — extend `internal/tmuxcmd/apply.go` `buildApplySteps` to add
   `set-hook -g client-session-changed` and `set-hook -g session-window-changed`
   steps that `run-shell "<bin> tmux record-nav --client=#{client_name} --pane=#{pane_id}"`.
   Mirror the existing `run-shell` hook/binding steps already built there; they're
   re-applied idempotently wherever `apply` runs today.
4. **goto marker** — in `src/endless/session_cmd.py` `session_goto`, set
   `@endless_nav_via=goto` via tmux `set-option` immediately before
   `_tmux_switch_client`. One-shot; the recorder clears it.
5. **Viewer** — `endless session trail` (`cli.py` registration +
   `session_cmd.py` logic). Lists recent edges for the current client
   (resolve `client_name`), newest-first: `from → to` as session id +
   `task_id_display(active_task)` + summary, the `via` tag, and relative time.
   The DB read goes through Go (add a `session-query` reader; reads-in-Go rule —
   no new Python SQLite reads). Default = current client; `--all` for every client.

## Tests

- **Go:** recorder resolves pane→session, forms `from→to` from the client's last
  row, reads/clears the `via` marker, and writes the row; migration applies and
  seeds `nav_via_kinds`.
- **Python:** `session trail` rendering (mock the Go query) — newest-first, `via`
  tag, task labels, empty-state.
- **`tests/tasks/e-1682-verify.sh`** (E-1681 model, non-disruptive): assert the
  migration/table exists; assert `apply` installs the nav hooks (check
  `tmux show-hooks -g`); drive the recorder against a synthetic pane on an
  **isolated** tmux server (`tmux -L …`) + sandbox DB and assert a row lands;
  assert `session trail` renders it; assert `goto` sets the marker and the
  recorder clears it.

## Verification

- `just build`; `just test` green.
- Manual, in tmux: switch between windows/sessions, run `endless session goto`,
  then `endless session trail` — edges appear with correct `via` tags and resolve
  to the right sessions/tasks.

## Out of scope

- Pruning / retention policy (revisit only if the table grows).
- pane-level (intra-window) focus capture.
- Jumping to a trail entry (`goto` a row) — a possible later enhancement.
