# Sessions: Recording Status, Discovering Yourself, Reading Cross-Session State

The session-status subsystem turns "what are you working on right now" from chat-table prose into queryable structured rows in the DB. Sessions write status snapshots; readers (other sessions, future-you, the web UI) consume them.

---

## What a session status is

Each row in `session_statuses` is a snapshot of one session's reported state at one moment:

- `task_id` — populated automatically by the handler from `sessions.task_id` at insert time; makes joins to `tasks` trivial. (A session holds one task, so there is no inactive one to distinguish it from.)
- `headline` — one-line summary of what just changed.
- `tasks` — every task the session is touching (resolved / pending / unverified / unreviewed, all in one column; the renderer derives the disposition bucket from each task's status).
- `decisions` — design choices, framings, insights too lightweight to be `endless decision add` items but worth capturing.
- `commits` — commit SHAs of work that didn't land via a task (manual hygiene, ledger splits, etc.).
- `memory` — entries created or modified in `~/.claude/projects/.../memory/`.
- `summary` — structured implementation breakdown (per-layer: name, files, purpose).
- `notes` — free-form prose for anything that doesn't fit a typed slot.

Latest row by `created_at` is the current status. Older rows are history — useful for "what did this session do over its lifetime" or "what's the activity trace for task E-NNN."

## Recording a snapshot: `endless session snapshot add`

> The verb is `snapshot` (renamed from `session status`) so `session status` can name the live work-state view. The recorded artifact is still a session-status snapshot.

```bash
endless session snapshot add <<'XML'
<session-status>
  <headline>One-line summary of what just changed.</headline>

  <tasks>
    <task id="E-101" status="confirmed">short title of the finished work</task>
    <task id="E-102" status="unverified" filed="true">something you filed and started</task>
    <task id="E-NNNN" status="ready">waiting on the user's review</task>
    <task id="E-103" status="unplanned">something that still needs a plan</task>
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

If you need your session's id outside the `session snapshot add` flow, the canonical Go-side helper is `monitor.GetLiveSessionByProcess(process string)`. From the CLI, `endless task id` prints the active **task** ID for the current pane — one bare `E-NNNN` line, exit 1 when there is none (same DB binding the tmux status row reads; `endless tmux task` is an alias).

Direct SQL lookup pattern (filter `state != 'ended'` to skip dead sessions in the same pane):

```bash
sid=$(endless sql --tsv "SELECT id FROM sessions
                         WHERE process='$TMUX_PANE' AND state != 'ended'
                         ORDER BY last_activity DESC LIMIT 1" | tail -1)
```

## Session state, and what the write gate actually refuses

A session row carries a **state**, and it moves on its own:

| State         | Meaning                                                                                  |
|---------------|------------------------------------------------------------------------------------------|
| `working`     | A turn is in progress.                                                                     |
| `prompted`    | Claude Code is asking your user to approve a tool call, and you are blocked mid-turn until they answer. Set by the `Notification` hook; cleared by your next activity — the approved tool completing, or their next message. |
| `idle`        | Between turns. Set by the `Stop` hook at the end of every turn; the next hook event of the next turn puts a session that holds a task back to `working`. |
| `needs_input` | You asked your user something and the answer has not arrived. Only their next message ends it — no command clears it. |
| `primed`      | You were started ahead of need, read the task in, asked your questions, and are holding for your user. Set by `endless session primed`, the last step of a read-in; kept through the `Stop` that ends that turn; cleared to `working` by your user's next message, which also re-checks the paths your plan cites. See **Primed sessions** in `endless guide orchestration`. |
| `ended`       | The session is over. An incoming hook event revives it to `idle`, because an event is proof it is alive. |

The four states in which a session is **waiting on a person** — `prompted`,
`idle`, `needs_input` and `primed` — are one group, `awaits-human`. Waiting means it has
paused for input, whether or not it asked a question; a finished turn and a
question are the same fact for this purpose. Ask that group rather than listing
states: `endless-go session-state get awaits-human`.

On a project with tracking in `enforce` mode, a **PreToolUse gate** stands in
front of the file-writing tools. The question it asks is *"has this session
declared what it is working on?"*, and the answer is `sessions.task_id` — set at
claim, write-once, true for the session's lifetime. So it admits a session that
**holds a task** and is `working`, `prompted` or `idle`.

`idle` is admitted deliberately. A write from an idle session is mid-turn by
construction — writes only happen inside turns — so the state is stale, not the
agent. The gate once admitted `working` alone, which meant a session that
completed one clean turn could never write again for the rest of its life.

`prompted` is admitted on the same reasoning: a session blocked on a permission
prompt is mid-turn on a tool call it already decided to make, and the tool it is
waiting on is its own. Refusing it would mean the write right after your user
clicks *approve* gets turned down.

Exactly two things are refused, and each says which one it is:

- **A session that has declared nothing** — no task, or no session row at all.
  The refusal lists the project's open tasks and names `endless task claim <id>`,
  which works from that state.
- **A session in `needs_input`.** It *has* declared its task; it is waiting on a
  person. The refusal says so and names no command, because there is none to
  run: your user's next message clears it.
  A `primed` session is refused the same way, and never meets it in practice:
  your user's message that resumes it clears the state before any tool runs.

Neither refusal offers `--force`, and neither should be answered with one.
Repairing a session field by demoting a task's status is not a fix.

## Orienting and inspecting sessions: `status`, `show`, `list`

Three read-only commands for self-orientation and for coordinating with sibling / child sessions — no snapshot required:

- **`endless session status`** — a one-shot view of your current focus: the focal task, its spawning (parent) task, sibling tasks worked by other sessions on the focal task, and any cross-session in-flight work, with blocked-by / blocks decorations. The cheap "where am I, who else is live" check. Add `--tree` for the do/plan backlog as an IDs-only tree in implementation order (nesting = order, siblings = parallelizable), or `--json` for the same rows as data.
- **`endless session show [ref]`** — details for one session (yours by default; pass an endless integer id or Claude UUID prefix for another). Reach for it when you're coordinating and need to inspect a specific sibling or child session.
- **`endless session list`** — recent sessions in the current project: one row per session with its id, a one-column state glyph (legend below the table), the task it's active on, its message count, and that task's title. The roster view for finding a sibling / child session's id to `show`. `--all-projects` widens it to every project (and adds a Project column); `--project <name>` picks another from anywhere. Sessions that never claimed a task have nothing to fill those last two columns, so they are omitted until you pass `--all` — they remain addressable by id throughout.

### The ordering graph: what to do before what

Below the task rows (after any `… N hidden` footer, before the fault row), `session status` and `session monitor` draw which of your session's tasks block or should precede which — so choosing the next task does not mean opening each one. `--graph` renders the graph alone; `--json` carries it as a `graph` object (nodes with their `in_flight` / `on_list` flags, edges, conflicts with their `source`, cycles, and the lines exactly as drawn), so read that rather than parsing the text.

```
E-101 => E-102 => E-103 => E-104
E-105 => E-106 | E-103
E-107 => E-106
E-108 -> E-109
<> E-110 | E-111 | E-112
E-113 <> E-114
```

| Notation | Reads |
|---|---|
| `A => B` | A **blocks** B: B cannot start until A is done. |
| `A -> B` | A **should precede** B (the `precedes` relation). Advisory; nothing is blocked. |
| `A => B \| C` | B and C both stand in that relation. `\|` binds tighter than either arrow, so `A => B \| C => D` reads `A => (B \| C) => D`; a group only ever claims what is true of every member. |
| `A <> B` | A and B **must not run at the same time**. Symmetric, and never chained: `A <> B` and `B <> C` do not mean `A <> C`. |
| `<> A \| B \| C` | A mutual-exclusion set: no two of these may run at the same time. |
| dim id | Already in flight (a session is on it), or a repeat of an id drawn on an earlier line. |
| `cycle: A, B` | The relations form a cycle. That is a data error to fix, never silently dropped. |

Which tasks appear: your session's rows, minus your own task, the spawning task, the parent row, anything in flight or owned by another session, hidden rows, phase `later`, and anything finished — then **plus** every open task that blocks one of those, even when that blocker is `later` or in flight, because it is the reason its dependent is not available. `unverified` and `unreviewed` blockers still block and appear; finished ones impose no order and do not. A task appears only when it has an edge of some kind; a session with none draws nothing — no header, no blank line.

Line order is derived, never authored: a topological order over both arrows, ties broken by the longest chain leading on, then lowest id — so the first line leads with the task to start on.

`<>` has two sources, drawn the same way (`--json` says which): a declared `conflicts_with` relation, and **detection** — two open tasks whose worktrees change a common path (committed on the branch or uncommitted). Detection reads a cache the `worktree-paths` background job keeps; the status view never runs git for it, so a cold or stale cache just means no detected `<>` for that task until the job's next pass (about a minute). Paths Endless itself writes — the ledger, `verbs.jsonl`, `LESSONS.md`, the task and decision mirrors under `.endless/tasks/` and `.endless/decisions/` — do not count. A pair already related by `=>` or `->` gets the arrow instead of `<>`.

### Quieting a noisy status view

A long-running session accumulates task rows it no longer cares about. `endless session hide --task <id>` (repeatable) drops them from **your** `session status` / `session monitor` view:

```bash
endless session hide --task E-101 --task E-102   # quiet two rows
endless session status                             # … 2 hidden (--show-hidden)
endless session status --only-hidden               # what did I hide?
endless session unhide --task E-101               # put one back
```

Three things this is deliberately **not**:

- It is not a property of the task. Hiding is scoped to the *(session, task)* pair — another session working the same task sees its own view unchanged, and `task next`, blocking relations and landing are all unaffected.
- It never expires. No status transition un-hides a row, `unverified` included; only `session unhide --task` does.
- It never hides silently. Whenever anything is suppressed the view carries a `… N hidden (--show-hidden)` footer, in `session monitor` too. `--show-hidden` renders everything with hidden rows marked ⊘; `--only-hidden` renders just the hidden set, which is how you find ids to unhide without having recorded them.

Pass a session reference (`endless session hide ES-101 --task E-101`) to hide for a session other than your own. Note that bare `session hide <ids...>` — no `--task` — is a different command: it hides whole SESSIONS from `session list`.

### Saying what your session's list holds

Capture is automatic: Endless records a row for every task your session claims, files or edits, and classifies **how** it entered your scope — its *relation*.

| Relation     | How the task got here                                     |
|--------------|-----------------------------------------------------------|
| `claimed`    | you claimed it                                            |
| `queued`     | you added it with `session task add` — decided work       |
| `surfaced`   | you filed it during this session                          |
| `revisited`  | you edited it or ran `touch` on it, but didn't claim it   |
| `referenced` | you only read it (reserved; no capture emits it yet)      |

Three verbs cover what automation can't reach:

```bash
endless touch E-101 E-102              # in scope; nothing about the task changes
endless session task add E-101 E-102   # decided work you haven't touched yet
endless session task remove E-101      # a capture that shouldn't have happened
```

- **`touch`** enrolls a task as `revisited` — scope entry, and nothing else. No field is written and no other session holding the task is notified. Reach for it when you looked at a task, or want it on screen while you work, and editing it would be a lie: rewriting `phase` to get the same display effect records a re-prioritization that never happened, and a later reader cannot tell it from a real one. It is top-level, not under `session task`, because it is typed far more often than the corrections there.
- **`add`** enrolls a task as `queued` — decided work. Nothing has happened to it, so no automatic capture would ever record it. Promotion is upgrade-only: a task you merely read or edited is strengthened, and your own claimed task stays `claimed` (reported, not an error). The same ladder runs the other way for `touch`: `revisited` is the weakest relation anything emits, so touching a task you claimed, queued or filed leaves the stronger relation alone.
- **`remove`** deletes the association — the touch and its relation — so `task show`'s "Touched by:" stops reporting it, and any hide on the same pair is cleared with it. There is no undo beyond touching the task again. Refused on your own claimed task: a claim cannot be dropped.

**`remove` is not the inverse of `hide --task`**, and the difference is the whole point: hide suppresses a row while *keeping* the association, so the touch that really happened stays on the record. Hide is for a capture that is real but noisy; remove is for one that was simply wrong.

`session status` tiers rows by relation. Decided work (`claimed` / `queued`, marked ⊕) leads among equally actionable rows; `referenced` rows (marked ·) sink below everything and render dimmed, so reads can never crowd out work. Relation never outranks actionability, though — a `queued` task parked in `later` still sits below the task you're actually working.

Two more kinds of row render dimmed because neither is actionable from your board: `⟳ doing` rows, which another live session is working, and the `↑ parent` row, in every phase — both even when they wear `◆`, since landing them is not this board's job. `☑ verify` rows stay bright — verifying is your user's action — and your claimed task never dims. A highlighted id (focus, duplicate, unblocked) keeps its full colour on a dimmed row.

### Focus, and a task on more than one board

Your session's **focused** task is the one it last claimed, filed or updated (including `touch`) — what the conversation is on right now, which is often not the task you claimed. Queueing or reading a task does not move focus. `session status` / `session monitor` highlight it: the id in inverse video, and `◼︎` between the type letter and the id. Your claimed task never shows `◼︎` — when it has focus, the colour says so, and in plain text no `◼︎` anywhere means the same thing. `--json` carries `focus` on the frame and `focused` on each row. A focused task is shown even if you hid it.

When the same task sits on several live sessions' boards, only its **owner** shows it — decided each time the board is drawn, never stored, so it corrects itself when a session ends:

1. The live session that filed it.
2. Otherwise, the only live session that updated it.
3. Otherwise (two or more live updaters) nobody owns it, and it stays on each board with the duplicate mark.

Updating a task never takes it from the session that filed it. Hiding it does give it up: a session that ran `session hide --task` on a task no longer counts as its filer or updater, so it neither owns the task nor makes it ambiguous — hide means "not mine". The exception is focus: if the hiding session has the task in focus again, it counts, because touching the task again is exactly when duplicate work is possible. The task's claiming session is left out: working the task you claimed is the expected case, and it already shows as ⟳ doing on other boards. Dead sessions own nothing. Your claimed task, its parent and its spawner are never hidden this way.

The **duplicate** mark — `◫`, and the id near-black on red (256-colour 232 on 160) — warns that another session may already have done real work on the task. It appears on both boards when a task is focused in one session and owned by another, and on every board when ownership is ambiguous. Stop and decide which session keeps it. `--json` carries `duplicate_work` and `owned_elsewhere` on every row, including rows the table omits.

The **unblocked** mark — `▷`, and the id near-black on green (232 on 118) — is news: a task that was blocked has had a blocker released (a terminal status), nothing still holds it, and nobody has claimed it. It stays until someone claims or spawns it; it is computed each draw, not delivered once. On one id, duplicate outranks unblocked, which outranks focus. `--json` carries it as `spawnable`.

## Interactive, user-run session commands

The `session` group also carries commands a human runs interactively — session navigation, the live-watch dashboard, history / search, and hide / unhide. These aren't part of an agent's working flow; they're documented in `endless guide appendix-a`, which you read only to point a user at one.

## Reading snapshots

Read commands are not implemented yet. Once shipped:

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
- **In-transaction lookups must use the dbQuerier** — calling `monitor.GetX` from inside an Execute handler deadlocks (single sqlite connection). The session-status handler already does this; if you add a new event kind that needs session lookup, use the in-tx variant pattern.

## Don't

- Don't write directly to `session_statuses` via `endless sql --write` once the CLI is in place. The CLI handles dedup, child-table insertion, and task_id resolution; raw SQL writes bypass all three.
- Don't include `endless task assume <id> --outcome` content in the headline — outcomes belong on the task itself, not on session-status snapshots.
- Don't try to encode "this task is filed by this session" in the parent row's columns — use the `filed="true"` attribute on the relevant `<task>` element. The renderer marks filed tasks visually.

## Post-mortem

If there was anything about your recording of this session which felt like there was no place to capture it, or if you had to capture it in a sub-optimal place, or if you have any other suggestions about how to improve process of recording session status then please add a task to review it. Add your suggestions to the task's analysis via `endless task update <id> --analysis '<text>'` (or `--analysis-file <path>` for long content). And please also tell the user that you added the task.

{{if .report_gate}}## Reading a session's raw draft: `endless session turn`

Every reply that goes through the minimizer persists the draft it was minimized
from. `endless session turn` prints that draft verbatim — no diff, no columns.
Keep the minimized reply in the adjacent tmux pane and compare by eye.

```bash
endless session turn                 # the raw draft behind the last reply
endless session turn 3               # three turns back
endless session turn --session ES-101
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