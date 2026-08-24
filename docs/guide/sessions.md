# Sessions: Recording Status, Discovering Yourself, Reading Cross-Session State

The session-status subsystem turns "what are you working on right now" from chat-table prose into queryable structured rows in the DB. Sessions write status snapshots; readers (other sessions, future-you, the web UI) consume them.

---

## What a session status is

Each row in `session_statuses` is a snapshot of one session's reported state at one moment:

- `task_id` — populated automatically by the handler from `sessions.task_id` at insert time; makes joins to `tasks` trivial. (Both columns were `active_task_id` until E-1969; a session holds one task, so there was no inactive one to distinguish it from.)
- `headline` — one-line summary of what just changed.
- `tasks` — every task the session is touching (resolved / pending / blocked / unverified, all in one column post-E-1318; the renderer derives the disposition bucket from each task's status).
- `decisions` — design choices, framings, insights too lightweight to be `endless decision add` items but worth capturing.
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
    <task id="E-NNNN" status="blocked">waiting on the user's review</task>
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

## Orienting and inspecting sessions: `status`, `show`, `list`

Three read-only commands for self-orientation and for coordinating with sibling / child sessions — no snapshot required:

- **`endless session status`** — a one-shot view of your current focus: the focal task, its spawning (parent) task, sibling tasks worked by other sessions on the focal task, and any cross-session in-flight work, with blocked-by / blocks decorations. The cheap "where am I, who else is live" check. Add `--tree` for the do/plan backlog as an IDs-only tree in implementation order (nesting = order, siblings = parallelizable), or `--json` for the same rows as data.
- **`endless session show [ref]`** — details for one session (yours by default; pass an endless integer id or Claude UUID prefix for another). Reach for it when you're coordinating and need to inspect a specific sibling or child session.
- **`endless session list`** — recent sessions in the current project: one row per session with its id, a one-column state glyph (legend below the table), the task it's active on, its message count, and that task's title. The roster view for finding a sibling / child session's id to `show`. `--all-projects` widens it to every project (and adds a Project column); `--project <name>` picks another from anywhere. Sessions that never claimed a task have nothing to fill those last two columns, so they are omitted until you pass `--all` — they remain addressable by id throughout.

### Quieting a noisy status view

A long-running session accumulates task rows it no longer cares about. `endless session hide --task <id>` (repeatable) drops them from **your** `session status` / `session monitor` view:

```bash
endless session hide --task E-1832 --task E-1902   # quiet two rows
endless session status                             # … 2 hidden (--show-hidden)
endless session status --only-hidden               # what did I hide?
endless session unhide --task E-1832               # put one back
```

Three things this is deliberately **not**:

- It is not a property of the task. Hiding is scoped to the *(session, task)* pair — another session working the same task sees its own view unchanged, and `task next`, blocking relations and landing are all unaffected.
- It never expires. No status transition un-hides a row, `unverified` included; only `session unhide --task` does.
- It never hides silently. Whenever anything is suppressed the view carries a `… N hidden (--show-hidden)` footer, in `session monitor` too. `--show-hidden` renders everything with hidden rows marked ⊘; `--only-hidden` renders just the hidden set, which is how you find ids to unhide without having recorded them.

Pass a session reference (`endless session hide ES-1041 --task E-1832`) to hide for a session other than your own. Note that bare `session hide <ids...>` — no `--task` — is a different command: it hides whole SESSIONS from `session list`.

### Correcting what your session's list holds

Capture is automatic: Endless records a row for every task your session claims, files or edits, and classifies **how** it entered your scope — its *relation*.

| Relation     | How the task got here                                    |
|--------------|----------------------------------------------------------|
| `goal`       | you claimed it                                           |
| `queued`     | you added it with `session task add` — decided work      |
| `surfaced`   | you filed it during this session                         |
| `revisited`  | you edited it, but it isn't your goal                    |
| `referenced` | you only read it (reserved; no capture emits it yet)     |

Two verbs cover what automation can't reach:

```bash
endless session task add E-1832 E-1902   # decided work you haven't touched yet
endless session task remove E-1832       # a capture that shouldn't have happened
```

- **`add`** enrolls a task as `queued`. Nothing has happened to it, so no automatic capture would ever record it — this is the only way it gets on your list. Promotion is upgrade-only: a task you merely read or edited is strengthened, and your own claimed task stays `goal` (reported, not an error).
- **`remove`** deletes the association — the touch, its relation, and its `session order` position — so `task show`'s "Touched by:" stops reporting it, and any hide on the same pair is cleared with it. There is no undo beyond touching the task again. Refused on your own goal task; release the task instead.

**`remove` is not the inverse of `hide --task`**, and the difference is the whole point: hide suppresses a row while *keeping* the association, so the touch that really happened stays on the record. Hide is for a capture that is real but noisy; remove is for one that was simply wrong.

`session status` tiers rows by relation. Decided work (`goal` / `queued`, marked ⊕) leads among equally actionable rows; `referenced` rows (marked ·) sink below everything and render dimmed, so reads can never crowd out work. Relation never outranks actionability, though — a `queued` task parked in `later` still sits below the task you're actually working.

## Interactive, user-run session commands

The `session` group also carries commands a human runs interactively — session navigation, the live-watch dashboard, history / search, and hide / unhide. These aren't part of an agent's working flow; they're documented in `endless guide appendix-a`, which you read only to point a user at one.

## Reading snapshots

Read commands are tracked under E-1319 (blocked by E-1318). Once shipped:

```bash
endless session snapshot latest [--session N]    # latest row for a session (defaults to current pane)
endless session snapshot show <id>               # render a specific row's markdown
endless session snapshot list [--session N] [--task E-NNN] [--limit N]
```

Until then, read directly via `endless sql`:

```bash
endless sql "SELECT id, session_id, task_id, headline, created_at
             FROM session_statuses ORDER BY id DESC LIMIT 5"
```

## Failure modes

- **Empty input** → CLI errors before emitting an event.
- **Validation error** (bad task id, unknown element, missing required attribute) → `click.ClickException`; nothing inserted.
- **Empty `TMUX_PANE`** → Go handler returns "no live session for process ''" error.
- **Identical to the latest row** → dedup-skip; row count unchanged; chat still gets the markdown echo.
- **In-transaction lookups must use the dbQuerier** — calling `monitor.GetX` from inside an Execute handler deadlocks (single sqlite connection). E-1315 fixed this for the session-status handler; if you add a new event kind that needs session lookup, use the in-tx variant pattern.

## Don't

- Don't write directly to `session_statuses` via `endless sql --write` once the CLI is in place. The CLI handles dedup, child-table insertion, and task_id resolution; raw SQL writes bypass all three.
- Don't include `endless task assume <id> --outcome` content in the headline — outcomes belong on the task itself, not on session-status snapshots.
- Don't try to encode "this task is filed by this session" in the parent row's columns — use the `filed="true"` attribute on the relevant `<task>` element. The renderer marks filed tasks visually.

## Post-mortem

If there was anything about your recording of this session which felt like there was no place to capture it, or if you had to capture it in a sub-optimal place, or if you have any other suggestions about how to improve process of recording session status then please add a task to review it. Add your suggestions to the tasks.analysis field via `endless task update <id> --analysis '<text>'` (or `--analysis-file <path>` for long content). And please also tell the user that you added the task.

{{if .report_gate}}## Reading a session's raw draft: `endless session turn`

Every reply that goes through the minimizer persists the draft it was minimized
from. `endless session turn` prints that draft verbatim — no diff, no columns.
Keep the minimized reply in the adjacent tmux pane and compare by eye.

```bash
endless session turn                 # the raw draft behind the last reply
endless session turn 3               # three turns back
endless session turn --session ES-1101
endless session turn B -p            # option B of a paired minimization, in full
```

The argument counts **turns back**, 0-based: no argument is the most recent turn,
`3` is three turns back. `A` and `B` instead address the two options of the most
recent paired minimization.

By default it reads the **sibling** Claude pane in your tmux window, not your
own: the point is to review somebody else's reply. `task report --raw` cannot
serve this — it resolves the calling session, so it can never reach another
session's draft.
{{end}}