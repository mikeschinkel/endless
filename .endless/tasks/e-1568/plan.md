# E-1568 — Add --bg flag to endless task spawn that dispatches via claude --bg --name carrier

Lands after **E-1565** (renderer), **E-1566** (type variants), **E-1571** (sessions: kind + active_epic_id), and **E-1569** (bgIsolation:'none' in settings). All four are landed (`assumed`). This is the centerpiece of the epic.

> **Plan corrected at pickup (2026-06-18)** against the real tree. Three deviations from the
> original draft, two adapted to reality and one decided with Mike:
> 1. E-1571 shipped `kind_id INTEGER` (FK → `session_kinds`, `1=tmux`/`2=background`), **not** a
>    `kind TEXT` column. Every `kind='background'` below is `kind_id=2`.
> 2. `session_gates` was dropped by `e-1582`; the only remaining FK to `sessions(session_id)` is
>    `session_messages` (ON DELETE CASCADE). Other FKs point at `sessions(id)`.
> 3. The schema change is a **`.go` change file** (runner, FK OFF), not `.sql`. The `.sql`
>    dispatcher runs on `monitor.DB()` (FK ON) inside `BEGIN IMMEDIATE`, where `PRAGMA
>    foreign_keys` is a no-op — a table rebuild can't work there. E-1571 hit this exact wall.
>
> Decisions confirmed with Mike: (a) **rebuild + nullable session_id** (not a placeholder hack);
> (b) **`{{if .bg}}` branch in the 4 per-type handoff templates** (not new files, not reuse-as-is).
>
> **Correction during verify (4th finding):** the original plan's standalone
> `CREATE UNIQUE INDEX ... ON sessions(short_id) WHERE short_id IS NOT NULL` in
> schema.sql is non-deployable. `monitor.DB()` re-applies schema.sql on every
> connection — including inside `endless db apply-change`, which opens it BEFORE
> running the change file. On a pre-E-1568 DB the `CREATE TABLE IF NOT EXISTS`
> no-ops on the existing old table, so a standalone index referencing `short_id`
> errors ("no such column"), and the migration can never apply (chicken-and-egg).
> Fix: an **inline `UNIQUE (short_id)` table constraint** (same semantics —
> SQLite treats NULLs as distinct, so many NULL short_ids coexist and non-NULL
> handles stay unique) that lives inside the skipped CREATE TABLE, keeping
> schema.sql a clean no-op on old DBs. The change file's rebuilt table carries
> the same inline constraint.

## Decisions

### CLI surface
- New flag `--bg` on `endless task spawn <id>`.
- `--worktree <path>` works in both fg and bg: in bg it sets `subprocess.run(..., cwd=<path>)`.
  Default remains the spawn-created worktree `.endless/worktrees/e-<id>/`.
- Compatible with `--reopen`, `--force`. `--no-plan` is a no-op for bg (no `/plan` slash concept) — document and ignore.
- Compatible with `--type` of the target task (renderer picks the right handoff variant).
- **tmux gate is skipped for `--bg`.** The fg path requires `$TMUX`; bg agents are headless, so
  the `not os.environ.get("TMUX")` guard is bypassed when `bg=True`.

### Invocation
Per `docs/research-2026-06-12-claude-background-agents.md` §2/§7:
```bash
claude --bg --name "E-<task_id>" "<rendered-handoff-text>"
```
Prompt is positional argv (handoff ~2 KB, well under Darwin ARG_MAX 256 KB). Stdout returns a
human-formatted block whose first line is `backgrounded · <8hex> · E-<task_id>` (no ANSI codes per §2).

### Short-ID capture
Parse the first stdout line matching `^backgrounded · ([a-f0-9]+) · ` (`re.M`, regex pinned in code
with comment + research-doc citation). On no-match, raise `ClickException` with the raw stdout —
never silently proceed with a missing id.

### Session row write (Go, not Python)
Per E-1486 (no new Python SQLite access), the row is written by a **new Go helper invoked via an
`endless-go` subcommand** — mirroring how `render_handoff` and `session-query ensure-claude-id`
already shell out. Python passes `task_id`, `short_id`; Go resolves `project_id` and the epic
ancestor itself and INSERTs:

| Column | Value |
|---|---|
| `session_id` | **NULL** — bg agents have no UUID at dispatch. SessionStart UPDATEs it later. UNIQUE treats NULL as distinct, so multiple pending bg rows coexist. |
| `project_id` | resolved from the task row |
| `platform` | `'claude'` |
| `state` | `'working'` |
| `active_task_id` | `<task_id>` |
| `active_epic_id` | nearest `type='epic'` ancestor via `WITH RECURSIVE` walk up `tasks.parent_id` (joined to `task_types`); NULL if none. **Resolved in Go**, not Python. |
| `kind_id` | `2` (`background`) |
| `process` | NULL |
| `short_id` | `<short_id>` from dispatch stdout |
| `started_at` | now |

`short_id` is the durable handle: `claude attach <short_id>` works; the row survives the UUID arriving later.

### SessionStart hook update (bg-context decoration)
New branch in `internal/hookcmd/claude.go` SessionStart handling, BEFORE the spawn-marker / cwd
auto-bind paths (bg agents have no meaningful tmux marker or binding cwd):

```go
if jobDir := os.Getenv("CLAUDE_JOB_DIR"); jobDir != "" {
    short := filepath.Base(jobDir)   // CLAUDE_JOB_DIR = ~/.claude/jobs/<short_id> per research §6
    // UPDATE sessions SET session_id=?, last_activity=? WHERE short_id=? AND kind_id=2
    // rows==0 → log + fall through to normal new-row path (defensive)
    // rows>=1 → return (decorated; skip auto-bind)
}
```
Uses the same `monitor.DB()` + parameterized `db.Exec` pattern as the other session UPDATEs.
A new `monitor.DecorateBgSession(shortID, sessionID string) (int64, error)` helper returns rows-affected.

### Status gate / throttling / window-name
Same done-ish gate as fg (`--force`/`--reopen` reuse existing code). Throttling is E-1572 — out of
scope; this child unconditionally dispatches. No fg tmux window; coordinator window keeps its name.

### Schema change — `.go` rebuild
`internal/schema/changes/e-1568-sessions-short-id-and-nullable-session-id.go` (mirrors the
`//go:build ignore` + `runner.Run` shape of `e-1571-...go`). One transaction, runner connection has
FK OFF:

1. `CREATE TABLE sessions_new (...)` — identical column set/order to current `sessions` **plus**
   `short_id TEXT`, with `session_id TEXT` (NOT NULL dropped), `UNIQUE (session_id)`, and the
   existing FKs (`project_id`, `active_task_id`, `active_epic_id` → tasks; `kind_id` → session_kinds).
2. `INSERT INTO sessions_new (<all existing cols>) SELECT <same> FROM sessions` (short_id defaults NULL).
3. `DROP TABLE sessions; ALTER TABLE sessions_new RENAME TO sessions`.
4. Recreate both `sessions_null_process_on_end_{update,insert}` triggers (DROP TABLE took them).

`sessions_new` carries inline `UNIQUE (session_id)` and `UNIQUE (short_id)` (no standalone index —
see the chicken-and-egg correction above). `session_messages` FK (→ `sessions(session_id)`)
re-resolves by name post-RENAME; FK-OFF prevents transient CASCADE during the swap.

Mirror the post-change shape in `internal/schema/schema.sql`: drop `NOT NULL` on `session_id`
(keep `UNIQUE`), add `short_id TEXT` + inline `UNIQUE (short_id)` (comment: harness-agnostic handle
paired with `platform`, and why inline rather than a standalone partial index).

### bg handoff templates
`render_handoff` gains a `bg: bool = False` param, passed into `vars_payload["bg"]`. Each of
`handoff/{task,bug,research,epic}.md.tmpl` gets a `{{if .bg}} … {{else}} … {{end}}` split around the
tmux-specific sections: the bg branch omits the `tmux switch-client` / `tmux move-window -t archive`
return lines and the "return to that session" framing, and instead tells the agent to do the work,
flip the task to `verify`, and stop (the user attaches later via `claude attach <short_id>`).

## Files

### New
- `internal/schema/changes/e-1568-sessions-short-id-and-nullable-session-id.go` — rebuild above.
- `tests/test_spawn_bg.py` — Python bg-dispatch tests.
- `internal/hookcmd/claude_bg_session_test.go` — SessionStart bg-decoration tests.
- `internal/monitor/bg_session_test.go` — RecordBgAgentSession / epic-walk / DecorateBgSession tests.
- `tests/tasks/e-1568-verify.sh` — runnable end-to-end verify script (E-1596 shape): schema shape,
  inline UNIQUE, record-bg-agent + epic resolution, fg/bg handoff split, old→new migration via
  apply-change. Self-contained (throwaway DBs via `endless-go --config-dir`); does NOT launch a
  live `claude --bg` (that smoke test is documented as a manual step in the script).

### Modified
- `internal/schema/schema.sql` — nullable `session_id`, add `short_id` + partial unique index.
- `src/endless/task_cmd.py` — `spawn_plan(..., bg=False)`; skip tmux gate for bg; bg branch after
  pre-claim (build `claude --bg --name E-<id>` argv + handoff positional, `subprocess.run(cwd=...)`);
  parse short id; call new `endless-go` subcommand to write the row; echo
  `Backgrounded E-<id> as <short_id>. Attach: claude attach <short_id>`. `render_handoff` gains `bg`.
- `src/endless/cli.py` — `--bg` click option on the `spawn` command; pass through to `spawn_plan`.
- `internal/monitor/session.go` — `RecordBgAgentSession(taskID int64, shortID string) (int64, error)`
  (resolves project_id + epic ancestor, inserts with `session_id=NULL`, `kind_id=2`) and
  `DecorateBgSession(shortID, sessionID string) (int64, error)`.
- `internal/hookcmd/claude.go` — CLAUDE_JOB_DIR bg-decoration branch.
- new `endless-go` subcommand wiring (e.g. `session-query record-bg-agent --task-id --short-id`)
  calling `RecordBgAgentSession`; locate the existing `session-query` command group and add the verb.
- `internal/templatecmd/templates/handoff/{task,bug,research,epic}.md.tmpl` — `{{if .bg}}` branch.

### Tests
- `tests/test_spawn_bg.py`: mock subprocess; assert `claude --bg --name E-N <handoff>` + correct `cwd`;
  parse short id from canned stdout; assert the row-write subcommand is invoked with the right args;
  `--bg --worktree /tmp` → `cwd=/tmp`; parse-failure → ClickException, no row write.
- `internal/monitor/*_test.go`: `RecordBgAgentSession` writes `session_id=NULL`, `short_id`,
  `kind_id=2`, `active_task_id`, and `active_epic_id` = parent epic (or NULL when no epic ancestor).
- `internal/hookcmd/claude_bg_session_test.go`: `CLAUDE_JOB_DIR=.../abc1234` + a bg row with
  `short_id='abc1234'`, `session_id=NULL`, `kind_id=2` → SessionStart (source=startup, fake UUID)
  UPDATEs that row's `session_id` + `last_activity`, no new row. No-match short_id → falls through.
- Regex unit test for the short-id parser (trailing whitespace, alternate id lengths).

## Verification

1. `just build && just test` pass.
2. Sandbox migration: `endless-sandbox init e-test --mode populated`, then
   `endless db apply-change internal/schema/changes/e-1568-sessions-short-id-and-nullable-session-id.go`.
   Probes: `SELECT "notnull" FROM pragma_table_info('sessions') WHERE name='session_id';` → `0`;
   `short_id` column present; duplicate non-NULL `short_id` rejected (UNIQUE) while multiple NULLs
   coexist; both `sessions_null_process_*` triggers present; `kind_id` FK intact.
   `tests/tasks/e-1568-verify.sh` automates all of this (and the apply-change chicken-and-egg path).
3. From a worktree-bound session, claim an epic, `endless task spawn --bg <child-id>` → returns
   near-instant, prints short id; `endless sql "SELECT session_id, kind_id, active_task_id,
   active_epic_id, short_id FROM sessions ORDER BY id DESC LIMIT 1" --db main` shows
   `session_id IS NULL`, `kind_id=2`, `short_id` populated, `active_epic_id` = the epic.
4. Wait for the bg agent's SessionStart; re-query → `session_id` now the real UUID, `short_id` unchanged.
5. `claude agents` lists the bg agent named `E-<task_id>`; `claude attach <short_id>` drops in.
6. `endless task spawn --bg --worktree /tmp <id>` → bg dispatches with cwd=/tmp; row written; no error.

## Sequence note
Required by **E-1570** (attach verbs read `short_id`) and **E-1572** (throttle counts `kind_id=2` rows).
