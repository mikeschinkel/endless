# Sessions: Recording Status, Discovering Yourself, Reading Cross-Session State

The session-status subsystem turns "what are you working on right now" from chat-table prose into queryable structured rows in the DB. Sessions write status snapshots; readers (other sessions, future-you, the web UI) consume them.

---

## What a session status is

Each row in `session_statuses` is a snapshot of one session's reported state at one moment:

- `active_task_id` — populated automatically by the handler from `sessions.active_task_id` at insert time; makes joins to `tasks` trivial.
- `headline` — one-line summary of what just changed.
- `tasks` — every task the session is touching (resolved / pending / blocked / unverified, all in one column post-E-1318; the renderer derives the disposition bucket from each task's status).
- `decisions` — design choices, framings, insights too lightweight to be `endless task --decision` items but worth capturing.
- `commits` — commit SHAs of work that didn't land via a task (manual hygiene, ledger splits, etc.).
- `memory` — entries created or modified in `~/.claude/projects/.../memory/`.
- `summary` — structured implementation breakdown (per-layer: name, files, purpose).
- `notes` — free-form prose for anything that doesn't fit a typed slot.

Latest row by `created_at` is the current status. Older rows are history — useful for "what did this session do over its lifetime" or "what's the activity trace for task E-NNN."

## Recording a snapshot: `endless session snapshot add`

> The verb is `snapshot` (renamed from `session status`, E-1688) so `session status` can name the live work-state view. The recorded artifact is still a session-status snapshot.

```bash
endless session snapshot add <<'XML'
<session-status>
  <headline>One-line summary of what just changed.</headline>

  <tasks>
    <task id="E-1208" status="confirmed">verbs.jsonl write-time commit</task>
    <task id="E-1314" status="unverified" filed="true">consolidate task disposition cols</task>
    <task id="E-NNNN" status="blocked">waiting on Mike's review</task>
    <task id="E-1302" status="unplanned">endless task id CLI</task>
  </tasks>

  <decisions>
    <decision>chose XML over markdown for input — deterministic parsing</decision>
    <decision>tasks.status already encodes disposition; no separate column needed</decision>
  </decisions>

  <commits>
    <commit sha="1e3bbfc">ledger split 1264 → 500/500/264</commit>
  </commits>

  <memory>
    <entry path="feedback_no_autonomous_remediation.md">on partial fail, report and ask</entry>
  </memory>

  <summary>
    <layer name="Schema" files="internal/monitor/migrate.go">V8 migration</layer>
    <layer name="Handler" files="internal/events/session_status.go">tx-scoped lookup, dedup, INSERT, render</layer>
  </summary>

  <notes>Free-form context. Catches skipped items, handoffs, anything unstructured.</notes>
</session-status>
XML
```

Or with a file:

```bash
endless session snapshot add path/to/status.xml
```

The CLI:

1. Parses the XML against the strict schema (root must be `<session-status>`; `<task>` requires `id` matching `E-NNN` and a valid `status`; `<commit>` requires `sha` matching `[0-9a-f]{7,40}`; `<entry>` requires `path`; `<layer>` requires `name` and `files`).
2. Resolves your session via `TMUX_PANE` → DB lookup (no Python SQLite reads — the whole resolution lives in Go).
3. Dedups against the latest row for your session — byte-equal in all content columns means "skip insert"; the markdown still echoes back so chat sees the summary.
4. INSERTs a parent row + child rows in `session_status_tasks` (one per `<task>` element) inside one transaction.
5. Renders the row as markdown back to stdout for chat display.

## What goes where

| Element | Stored in | Notes |
|---|---|---|
| `<headline>` | `session_statuses.headline` | Plain text. One sentence. |
| `<tasks>` / `<task>` | `session_status_tasks` (child) | Each `<task>` becomes one child row. |
| `<decisions>` / `<decision>` | `session_statuses.decisions` (JSON array) | Free-text design choices. |
| `<commits>` / `<commit sha=...>` | `session_statuses.commits` (JSON array) | Commits without task IDs (manual hygiene). |
| `<memory>` / `<entry path=...>` | `session_statuses.memory` (JSON array) | Memory-file changes. |
| `<summary>` / `<layer name= files=>` | `session_statuses.summary` (JSON array) | Per-layer implementation breakdown. |
| `<notes>` | `session_statuses.notes` | Free-form. Skipped items, handoffs, prose. |

## When to call it

The natural attach points:

- **End-of-turn summaries** — when you'd otherwise write a "Final state" markdown table in chat.
- **Post-land moments** — right after `just land` succeeds for a task you owned.
- **Phase shifts** — moving from one task family to another, especially when leaving things in `unverified` for the user.
- **Discovering structural change** — a new design framing that should outlive this conversation.

If you're producing a chat table that maps to `tasks` / `decisions` / `commits` / `memory` / `summary` columns: that's the signal — convert it to XML and call the CLI instead. The chat output is duplicated automatically by the CLI's markdown echo.

## Discovery: who am I?

Sessions are bound to tmux panes via the `sessions.process` column (named "process" for harness-agnosticism; today it holds tmux pane IDs like `%124`).

If you need your session's id outside the `session snapshot add` flow, the canonical Go-side helper is `monitor.GetLiveSessionByProcess(process string)`. From the CLI, `endless-tmux active-id` returns the active task ID for the current pane (same DB binding the tmux status row reads).

Direct SQL lookup pattern (filter `state != 'ended'` to skip dead sessions in the same pane):

```bash
sid=$(endless sql --tsv "SELECT id FROM sessions
                         WHERE process='$TMUX_PANE' AND state != 'ended'
                         ORDER BY last_activity DESC LIMIT 1" | tail -1)
```

## Reading status (forward-looking — E-1319)

Read commands are tracked under E-1319 (blocked by E-1318). Once shipped:

```bash
endless session snapshot latest [--session N]    # latest row for a session (defaults to current pane)
endless session snapshot show <id>               # render a specific row's markdown
endless session snapshot list [--session N] [--task E-NNN] [--limit N]
```

Until then, read directly via `endless sql`:

```bash
endless sql "SELECT id, session_id, active_task_id, headline, created_at
             FROM session_statuses ORDER BY id DESC LIMIT 5"
```

## Failure modes

- **Empty input** → CLI errors before emitting an event.
- **Validation error** (bad task id, unknown element, missing required attribute) → `click.ClickException`; nothing inserted.
- **Empty `TMUX_PANE`** → Go handler returns "no live session for process ''" error.
- **Identical to the latest row** → dedup-skip; row count unchanged; chat still gets the markdown echo.
- **In-transaction lookups must use the dbQuerier** — calling `monitor.GetX` from inside an Execute handler deadlocks (single sqlite connection). E-1315 fixed this for the session-status handler; if you add a new event kind that needs session lookup, use the in-tx variant pattern.

## Don't

- Don't write directly to `session_statuses` via `endless sql --write` once the CLI is in place. The CLI handles dedup, child-table insertion, and active_task_id resolution; raw SQL writes bypass all three.
- Don't include `endless task assume <id> --outcome` content in the headline — outcomes belong on the task itself, not on session-status snapshots.
- Don't try to encode "this task is filed by this session" in the parent row's columns — use the `filed="true"` attribute on the relevant `<task>` element. The renderer marks filed tasks visually.

## Post-mortem

If there was anything about your recording of this session which felt like there was no place to capture it, or if you had to capture it in a sub-optimal place, or if you have any other suggestions about how to improve process of recording session status then please add a task to review it. Add your suggestions to the tasks.analysis field via `endless task update <id> --analysis '<text>'` (or `--analysis-file <path>` for long content). And please also tell the user that you added the task.