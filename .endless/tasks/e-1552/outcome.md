# Decisions

## CLI surface
Spawn's CLI flow stays uniform across all 24 type×children-state cells; the handoff template branches by type. Same pattern endless already uses for type-conditional behavior (verb gates, completable-status gates).

## Per-type spawn behavior
| Type | Spawn behavior | Handoff variant | End-state |
|---|---|---|---|
| `task` | Today's fg tmux + claim | `handoff/task.md.tmpl` | `verify` → confirmed |
| `bug` | Same as task; reproduce-first framing | `handoff/bug.md.tmpl` | `verify` → confirmed |
| `research` | Same as task; findings-as-deliverable | `handoff/research.md.tmpl` | `completed` (verb gate per ED-1502) |
| `epic` | Fg tmux coordinator + children-state summary | `handoff/epic.md.tmpl` | `completed` via auto-derivation (E-1537 §4) |

All handoffs include the child count and recommend `--children` to the spawned session.

## Bg-agent dispatch (epic children)
`endless task spawn --bg <id>` dispatches via `claude --bg --name "E-<task_id>"`. Spawn captures the short ID from stdout, writes the binding row directly to `sessions`. No DB-token mechanism, no env-var carrier — `--name` IS the carrier. SessionStart hook still fires inside the bg agent (source=`startup`); decorative for binding, not load-bearing.

## bgIsolation requirement
`worktree.bgIsolation: "none"` REQUIRED for v1. Added to `just claude-settings-init` output. Prevents Claude from creating a nested worktree under `.claude/worktrees/` that would defeat endless's pre-claim. Long-term alternative (`WorktreeCreate` hook) deferred until Agent View exits research preview, given current open-issue density (~11 open issues, two June 2026 regressions including the critical #68214 isolation-failure bug).

## DB schema additions
- `sessions.active_epic_id` (nullable FK to tasks).
- `sessions.kind` discriminator: `tmux` or `background`. `tmux_pane` nullable for background.
- Window-name rendering: `active_epic_id NOT NULL AND != active_task_id` → `[E-EEEE:E-CCCC]`; `NOT NULL AND =` → `[E-EEEE]`; `NULL` → `[E-NNNN]`.
- "Focused bg agent" is derived from the coordinator row, not stored.

## Attach verbs
- `endless task spawn --attach <id>` — new tmux window via `tmux new-window` + send-keys `claude attach <short-id>`. Used when a bg agent needs dedicated focus.
- `endless task attach <id>` — exec current pane via `os.execvp("claude", ["attach", short_id])`. Used from a fresh shell.

Both leverage `claude attach` (Claude's bg→fg view mechanism per docs/research-2026-06-12-claude-background-agents.md §3). Detach paths leave the bg agent running; only `claude stop` or `Ctrl+X` ends it.

## Templates
Migration from Python `string.Template` to **Go `text/template`**. Renderer: hidden `endless internal template render <name>` subcommand reading JSON vars on stdin. Location: `templates/` top-level. Per-type at `templates/handoff/{task,bug,research,epic}.md.tmpl`. Coordinator templates SET at `templates/coordinator/{explore-child-context,plan-child,dispatch-child-impl,verify-child}.md.tmpl`. Endless owns the template SET; coordinators can override locally but default to template.

## Throttling
Soft warning at dispatch when N bg agents already active for the project (default threshold 3, configurable as `bg_throttle_warn` in `.endless/config.json`). Does NOT block dispatch — coordinator decides. Rationale: bg quota burns linearly with parallel count (10× for 10), community sweet spot 3–5.

## Children-state computation (epic handoff)
At render time: `SELECT status, count(*) FROM tasks WHERE parent_id = ? GROUP BY status`. Coordinator picks operational mode in-session from the breakdown:
- **Zero**: drive decomposition (plan-mode + AskUserQuestion + parallel Explore sub-agents).
- **All `needs_plan`**: Model 1 — planning orchestrator drafts plans inline.
- **All `ready`**: Model 2 — dispatcher fans out via `endless task spawn --bg <child-id>`.
- **All `in_progress`**: observe, no dispatch.
- **All terminal**: ask whether to reopen.
- **Mixed**: surface breakdown, ask user.

No CLI `--mode` flag; coordinator decides in-session.

## Session lifecycle
Bg agents survive: fg Claude exit, terminal close, tmux pane close, machine sleep (v2.1.142+). Die on: machine shutdown, 1h idle (unpinned), `claude stop`. Recovery commands filed as E-1560 (reboot via `claude respawn`) and E-1561 (tmux crash via reconnect).

## Relationship to E-1136
Co-exist. E-1552 = interactive coordinator. E-1136 = unattended daemon. Different operational shapes, not alternatives.

## One-session-per-task invariant
Stays as today's assumption. Open to revisiting when a concrete use case bends it.

# Implementation

**Follow-up epic:** **E-1564** — Implement type-aware endless task spawn and bg-agent dispatch from E-1552 design.
Blocked-by E-1538 (task_types FK seeding).

**Children of E-1564:**
- E-1565 — Migrate spawn handoff templates from Python string.Template to Go text/template renderer
- E-1566 — Add per-type handoff variants for task/bug/research/epic
- E-1567 — Compute and inject children-state summary into epic handoff at render time
- E-1568 — Add --bg flag to endless task spawn that dispatches via claude --bg --name carrier
- E-1569 — Add worktree.bgIsolation:'none' to just claude-settings-init output
- E-1570 — Add endless task spawn --attach and endless task attach for bg-agent viewing
- E-1571 — Add sessions.active_epic_id column and kind discriminator for bg-agent rows
- E-1572 — Add soft warning at endless task spawn --bg when N bg agents already active
- E-1573 — Document spawn/coordinator/bg-dispatch patterns in endless guide orchestration section

# Related tasks filed during this work

**Maybe-phase** (long-term direction, no commitment):
- E-1558 — Extend endless sessions table to support non-Claude agent harnesses
- E-1559 — Implement WorktreeCreate hook integration to bridge Claude worktrees to endless

**Next-phase** (real follow-ups, not blocking):
- E-1560 — Add endless session restore to recover bg agents after machine shutdown/reboot
- E-1561 — Add endless tmux restore to reconnect to bg agents and respawn coordinator after tmux crash
- E-1574 — Document research-task model (text=request, outcome=results) in endless guide

# Decision recorded

ED-1507 — Research-type tasks: text holds the request, outcome holds the results. (`endless decision show ED-1507 --db main`; links --about E-1552.)