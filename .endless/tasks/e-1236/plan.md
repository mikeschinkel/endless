# E-1236: Add Endless tmux integration — ephemeral apply + runtime printer

## Context

Endless tracks the active task per session (`sessions.active_task_id`), but that information is invisible inside tmux unless a Claude session surfaces it. The result: when Mike is in a tmux window, he can't see at a glance which task he's on, and he can't trigger task operations from tmux without retyping commands. He has also requested that Endless eventually drive a separate tmux "menu bar" for fast task actions, anchored to the same status row.

This task adds the **basic** tmux integration: a second tmux status line that shows the current task ID, plus hotkey- and right-click-triggered popup menus for common task operations. The status line text is driven by an `#(shell-command)` substitution in `status-format[1]`, polled at `status-interval`. The user runs one command per tmux server start to wire it all up — no permanent config files are touched.

The narrower scope (no permanent install, no companion file, no `.tmux.conf` editing) is intentional: ship the ephemeral path first, validate the UX, defer the install-mode design until users with hand-crafted `.tmux.conf` setups actually exist.

## Two verbs — final shape

| Verb | Who calls it | Effect |
|---|---|---|
| `endless tmux apply` | **End user**, once per tmux server start | Issues a batch of `tmux` commands against the running server: enable second status line, set `status-format[1]` to call the printer, install hotkey + mouse menu bindings. **No file I/O.** Reverses on tmux server restart. |
| `endless tmux status-line` | **Tmux**, on each `status-interval` refresh | Reads tmux pane env + DB, prints one styled line of text to stdout. <50ms target. Always exits 0. |

Short verb for the human (typed every tmux start); longer verb for the runtime invocation (configured once, never typed).

## What `endless tmux apply` does (the apply command)

A single command issues this sequence of `tmux` calls against the running server:

1. **Enable second status line:**
   `tmux set-option -g status 2`

2. **Wire status-format[1] to the printer:**
   `tmux set-option -g status-format[1] '#[fg=colour245]#(endless tmux status-line)#[default]'`

3. **Tighten refresh cadence (per design brief):**
   `tmux set-option -g status-interval 2`

4. **Install hotkey-triggered popup menu (case b):**
   - `tmux bind-key e display-menu -T "Endless — E-NNNN" -x M -y W <items>` (prefix + `e`).
   - Items follow the idioms in `~/.init/tmux/conf.d/70-mouse-menus.conf`: hotkey-letter shortcuts in brackets, `''` separators, conditional dimming via `#{?...,,-}`, `run-shell` for actions, `command-prompt` where input is needed.

5. **Install right-click popup menu (case c, best-effort):**
   - `tmux bind-key -n MouseDown3StatusRight display-menu -T "Endless — E-NNNN" -x M -y W <same items>`.
   - Anchors to the **status-right region of the first status line** because tmux does not provide per-region mouse events scoped to `status-format[N>0]`. A child task can investigate `#[push-default]`/`#[mouse]` workarounds for finer-grained click regions on row 1.
   - `M-MouseDown3StatusRight` variant for Alt+right-click consistency with the user's existing menus.

6. **Issue `tmux refresh-client -S`** to redraw immediately (per design brief recommendation).

All six are idempotent — re-running `endless tmux apply` overwrites the same options/bindings; no duplication.

### Minimal v1 menu content

Deliberately thin so future child tasks can flesh it out. The task title (omitted from the bar) appears in the Detail popup.

```
Endless — E-NNNN
─────────────
[d] Detail         — run-shell "endless task show E-NNNN | tmux display-popup -E -"
[v] Mark verify    — run-shell "endless task update E-NNNN --status verify"
[t] Task tree      — run-shell "endless task list --tree | tmux display-popup -E -"
                  ''
[r] Refresh        — refresh-client -S
```

The menu title `Endless — E-NNNN` is interpolated at menu-build time from the same DB lookup the printer uses (see "How the printer resolves current task" below). No new CLI surface is needed; menu actions invoke existing `endless task` verbs.

## What `endless tmux status-line` does (the printer)

- Reads `TMUX_PANE` from env (set by tmux for any `#(…)` substitution).
- Resolves tmux pane → Endless session → active task (resolution chain below).
- Formats a one-line string with tmux `#[…]` style escapes. **v1 shape — task ID only, no title** (bar space is scarce; title lives in popups):
  ```
  #[fg=colour39][E-NNNN]#[default] #[fg=colour245]· <project> · <status>#[default]
  ```
- If no active task for this pane's session: print a quiet placeholder, e.g. `#[fg=colour240]·#[default]` (single dim dot) so the bar doesn't flicker or shift width.
- Always exits 0 (non-zero in `#()` shows an ugly empty bar).
- Suppresses stderr.
- Latency target <50ms.

## How the printer resolves "current task"

**Mechanism is established — no new design needed.**

- The DB schema (`internal/schema/schema.sql:57`) defines `sessions.active_task_id INTEGER` with `FOREIGN KEY (active_task_id) REFERENCES tasks(id) ON DELETE SET NULL`.
- It is set by `endless task claim <id>` (via `internal/monitor/session.go:35-38`) and cleared by `endless task confirm` / `task assume` / `task decline` paths (Python `complete_item` + hook `PostToolUse` regex; reconciliation tracked in **E-1142**).
- Resolution chain:

  ```
  $TMUX_PANE (env)
    → tmux pane UUID (via internal/monitor/tmux.go:GetTmuxContext)
    → sessions row (via tmux_pane_uuid match)
    → sessions.active_task_id
    → tasks row (id, status, title for popup, project)
  ```

- Single indexed SQLite read once the session is located. The same path serves both the printer (renders ID) and the menu (renders ID in title + title in the Detail popup).
- **Per house rule (no Python SQLite reads in new code):** the new query path lives Go-side in `internal/web/queries.go` style, not Python `db.query`. Implementation must designate which existing query helper to extend or which new one to add.

## Implementation sequencing

1. **Go-side query helper:** add a `GetActiveTaskForTmuxPane(paneUUID string)` (or equivalent) in `internal/web/queries.go` or a new sibling file. Returns task ID, status, title, project, or "no active task" sentinel. Single indexed SQLite read.
2. **`endless tmux status-line` Go command:** new `cmd/endless/tmux/` (or equivalent location matching current Go layout). Reads `TMUX_PANE`, calls the query helper, emits the styled one-liner. Always exits 0.
3. **`endless tmux apply` Go command:** issues the six `tmux` calls. Shells out to the `tmux` binary; no library dependency.
4. **Python CLI wiring:** `src/endless/cli.py` registers the new `tmux` group (with `apply` and `status-line` subcommands) as thin wrappers that shell out to the Go binary. Per the "no Python SQLite reads" rule, Python doesn't touch the DB here.
5. **Manual smoke test** in a real tmux server (verification section below).

Steps 1–3 are independent; 4 depends on the binaries existing. Build order: Go first (`just build`), then test the binaries from the shell, then add the Python wrappers.

## Out of scope (explicitly — deferred or already filed)

- **Window-tab attention notifications** — Mike's existing `~/.init/` scripts (`claude-tmux-alert`, `dismiss-alert.sh`) color/prefix tmux window tabs on Claude notifications. Moving these into Endless is **already filed as E-719** (`needs_plan`, phase `next`). Do not duplicate here.
- **Mike's personal tmux config** — `~/.init/tmux/conf.d/*` belongs to Mike, not Endless. The apply command operates on the running tmux server (set-option / bind-key); it does not touch Mike's config files. If/when patterns from Mike's config should become Endless features, that's a separate decision deferred until Endless has users with their own tmux configs.
- **Permanent install** — companion file at `~/.config/endless/tmux-status.conf` + a `setup tmux-status` install verb (with auto-edit / manual / print-only modes). Future child task.
- **Lineage breadcrumb in status text** — consumes E-1128 (spawn lineage).
- **Sibling-window task summary** — consumes E-1125 (lineage CLI primitives).
- **Session focus title `EF-NNN` in status text** — consumes E-1031 (session focus).
- **Color/state theming** — in_progress=green, verify=yellow, blocked=red, etc.
- **Configurable format string** in `.endless/config.json`.
- **Richer menu content** — more items, dynamic per task state.
- **Per-region mouse events on status-format[1]** — investigate `#[push-default]`/`#[mouse]` for clickable elements within the second status line itself, not just anchored to status-right of row 0.
- **Truncation/collapse logic** for narrow tmux panes.
- **Push-based updates** — Endless calls `tmux set-option` directly on state changes (per design brief; defer until polling proves laggy).

## Critical files

- `src/endless/cli.py` — register a new `tmux` command group (alongside the existing setup-group region at `cli.py:1481+`) with `apply` and `status-line` subcommands.
- `src/endless/tmux_cmd.py` *(new)* — thin Python wrapper that shells out to the Go binary for both verbs.
- `internal/monitor/tmux.go` — already exposes `GetTmuxContext()`; reuse as-is for pane UUID resolution.
- `internal/monitor/session.go:104` — existing `SELECT … active_task_id …` query; the new pane→task helper lives nearby.
- `internal/schema/schema.sql:57` — confirms `sessions.active_task_id` schema; no schema changes for this work.
- `cmd/endless/tmux/` *(new)* — Go-side implementation for both verbs. Final location matches current Go cmd layout.

## Existing utilities to reuse

- `internal/monitor/tmux.go:GetTmuxContext` — tmux session/window/pane UUID resolution. Do not re-implement.
- `sessions.active_task_id` (`schema.sql:57`) — the authoritative "what is the current task for this session?" field. Do not invent a parallel mechanism.
- Existing session→task query patterns in `internal/monitor/session.go` and `internal/web/queries.go` — extend or mirror; do not duplicate.
- Mike's menu idioms in `~/.init/tmux/conf.d/70-mouse-menus.conf` — visual/structural template for `display-menu` definitions: `-T` title, `-x M -y W` positioning, hotkey-letter shortcuts, `''` separators, conditional dimming, `run-shell` actions.

## Verification

End-to-end smoke test inside a real tmux server:

1. **Setup:** in a fresh tmux server with a session attached to an Endless project worktree, run `endless task claim E-XXXX` for some test task.
2. **Apply:** run `endless tmux apply`. Confirm:
   - Status row 2 appears (second line of status bar).
   - Row 2 shows `[E-XXXX] · <project> · <status>`.
   - Status updates within ~2 seconds of `endless task confirm E-XXXX` (`status-interval=2`).
3. **Hotkey menu:** press `<prefix> + e`. Confirm a popup menu titled `Endless — E-XXXX` appears with the four v1 items.
4. **Menu actions:**
   - `[d] Detail` — opens a popup with `endless task show E-XXXX` output.
   - `[v] Mark verify` — flips task to `verify`; status-line `<status>` field updates after `status-interval`.
   - `[t] Task tree` — opens a popup with `endless task list --tree` output.
   - `[r] Refresh` — triggers immediate `refresh-client -S`.
5. **Right-click menu:** right-click on the status-right region of row 0. Confirm the same menu appears. Repeat with Alt+right-click.
6. **No active task:** run `endless task confirm E-XXXX` (clearing `active_task_id`). Confirm row 2 shows the single dim-dot placeholder, not a flicker or empty bar.
7. **Tmux restart:** restart the tmux server (`tmux kill-server`; start again). Confirm row 2 is gone — verifies the "ephemeral" property.
8. **Re-apply idempotence:** run `endless tmux apply` twice in succession; confirm no error and no duplicate bindings (`tmux list-keys` shows single entries).
9. **Latency:** time `endless tmux status-line` standalone; confirm <50ms (target).

If any of 1–6 fail, the parent ships in `verify`; Mike confirms after a real workday using the bar.
