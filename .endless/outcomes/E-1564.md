All nine implementation children shipped and assumed; the type-aware spawn + bg-dispatch system is in production:

- **E-1565** — Go `text/template` renderer behind hidden `endless internal template render`; embedded `templates/` materializes per-file into `<project_root>/.endless/templates/` with `.local.tmpl` precedence.
- **E-1566** — per-type handoff variants at `templates/handoff/{task,bug,research,epic}.md.tmpl`, selected by `tasks.type_id` slug.
- **E-1567** — children-state breakdown injected into the epic handoff at render time, plus the operational-mode guidance block.
- **E-1568** — `endless task spawn --bg` dispatches via `claude --bg --name "E-<id>" "<handoff>"`, captures short id from stdout, writes `sessions` row with `kind_id=2 (background)`, `session_id=NULL` at dispatch (UPDATEd to real UUID by the bg agent's SessionStart hook); same change file adds `short_id TEXT` column and relaxes `session_id NOT NULL` via table rebuild.
- **E-1569** — `worktree.bgIsolation:'none'` injected into worktree-local `.claude/settings.json` by `just claude-settings-init`.
- **E-1570** — `endless task spawn --attach` (new tmux window) and `endless task attach` (current pane exec via `os.execvp`).
- **E-1571** — `sessions` schema additions: `active_epic_id` (FK to tasks), `kind_id` (FK to new `session_kinds` values table — diverged from the originally-planned `kind TEXT` per house no-CHECK rule).
- **E-1572** — soft stderr warning at `endless task spawn --bg` when ≥ `bg_throttle_warn` (default 3) bg agents already active per project. Does not block.
- **E-1573** — `endless guide orchestration` rewritten to document per-type spawn, `--bg` dispatch, attach verbs, coordinator pattern, children-state operational modes, throttle, lifecycle, and the `.endless/templates/<name>.local.tmpl` override convention.

Maybe-phase follow-ups reparented out of E-1564: **E-1581** (user-level template overrides at `~/.config/endless/templates/`) and **E-1597** (handoff templates keyed on type+ground dimensions). Both stand alone awaiting concrete need.
