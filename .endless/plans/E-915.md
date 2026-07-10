# Plan: Enforce Task and Decision Recording via Hooks

## Context

Per `docs/TASK_RECORDING_PROMPT.md`, Claude Code sessions on Endless repeatedly fail to record tasks before code changes and fail to record decisions when they emerge from discussion. Text-only reminders (CLAUDE.md, memory, session guide) are demonstrably insufficient: Claude knows the rule but does not consistently follow it.

Exploration revealed the enforcement substrate is more complete than the prompt suggested:

- A `PreToolUse` hook in `cmd/endless-hook/claude.go:256` already blocks `Write`/`Edit`/`NotebookEdit` when no working session exists, *if* the project's tracking mode is `enforce`.
- Endless defaults to `enforce` for registered projects (`internal/monitor/session.go:327`); `.endless/config.json` does not currently override it.
- `endless task start <id>` invokes `monitor.StartWorkSession()` and writes `active_task_id` per `session_id` to the `sessions` table.

Three real gaps remain, in order of impact:

1. **Task drift is the dominant cause of unrecorded changes.** PreToolUse only checks "is there *any* active task." Once Claude starts task A, every subsequent edit passes, even if Claude has switched focus to unrelated work. This is the workflow Endless was built to track (constantly switching focus without losing prior context), so detecting drift at the edit boundary is first-class.
2. **Decisions are invisible to PreToolUse.** Decisions emerge from chat and do not trigger Edit/Write, so the existing block has nothing to fire on. There is no enforcement mechanism for `--type decision` tasks today.
3. **No post-session verification.** The `SessionEnd` handler at `cmd/endless-hook/claude.go:158` only parses the transcript and ends the session. Edits attributed to no task slip through silently.

The intended outcome is defense in depth: PreToolUse catches drift in real time, Stop catches decisions, SessionEnd catches anything that slipped through both.

## Design Notes

**Calibration as a first-class feature.** Each enforcement check is intentionally strict and asks Claude (in the same prompt) to optionally include a banner suggesting how the rule could be relaxed. Suggestions are persisted to a `suggestions` table and surface inline in the chat (visible to Mike at the moment they happen) plus as a count at next SessionStart. No suggestion auto-applies; Mike is the only one who can turn a suggestion into a code or config change. This makes the system get smarter from real-world use rather than from upfront guesses, and it ensures Claude cannot weaken its own enforcement.

**No dismiss action on suggestions.** AI output is non-deterministic, so reliably comparing a fresh suggestion against prior dismissals would require an AI call per hook (too expensive). Suggestions accumulate as a backlog; Mike accepts the worthwhile ones and leaves the rest. If accumulation becomes unwieldy, a `mute` action can be added later.

**Why ask Claude instead of doing forensics.** The hook already sees every Edit/Write through PostToolUse. We have an authoritative edit log natively, no `git diff` needed. And Claude knows what work it just did, so prompting Claude to attribute edits in real time beats post-hoc reconstruction.

## Recommended Approach

Three layers, each independently shippable. Together they form one tight mechanism around the per-task edit-set.

### Layer A: Per-Task Edit-Set and Drift Detection (PreToolUse)

**Goal**: every Edit/Write fires against an explicit, narrow active task. New files outside the task's scope force Claude to switch tasks, create a sub-task, or extend scope before the edit lands.

**New concept**: a per-task edit-set. New table `task_files` (`task_id`, `file_path`, `first_edited_at`, `first_edited_session_id`). Populated by PostToolUse on first edit of a file within an active task; subsequent edits to the same file are no-ops on the table.

**PreToolUse logic** (extends `cmd/endless-hook/claude.go:256`):

1. Existing block: no active session → block as today.
2. Active session exists → resolve target file path from `payload.ToolInput`.
3. Pass if any of: file is in `task_files` for the active task, file path appears in the active task's `tasks.text`, or file path appears in the linked plan file (`sessions.plan_file_path` or task-attached plan).
4. Otherwise block (`exit 2` + stderr) with this message:

   > Editing `<path>` but it is not in scope of active task E-Y "<title>". Choose:
   >
   > - `endless task start <id>` to switch focus (active task is preserved)
   > - `endless task add "<short title>" --parent E-Y` for a sub-task
   > - `endless task touch E-Y --add-file <path>` if `<path>` is genuinely part of E-Y's scope
   >
   > If you believe this prompt is wrong here, also include a banner in your next response:
   >
   > `**SUGGESTION (drift_detection):** <one-line explanation of why this should not have blocked>`

The post-hook (UserPromptSubmit of the next turn) parses the prior assistant turn for the banner and writes any captured suggestion to the `suggestions` table.

**Strict scope check by design**: file membership requires literal appearance in `task_files`, `tasks.text`, or the linked plan file. Loose heuristics (directory-prefix match, language-of-file match) are deliberately not in v1; they emerge from the suggestions log if the data warrants.

**Files to modify**:
- `cmd/endless-hook/claude.go:256` (extend `handlePreToolUse`)
- `cmd/endless-hook/claude.go:331` (extend `handlePostToolUseSession` to record `task_files` entries)
- `cmd/endless-hook/claude.go:118` (extend `UserPromptSubmit` to parse banners from prior assistant turn)
- `internal/monitor/files.go` (new): `RegisterTaskFile(taskID, sessionID, filePath)`, `IsFileInTaskScope(taskID, filePath) bool`
- `internal/monitor/suggestions.go` (new): `RecordSuggestion(sessionID, source, triggerCtx, suggestion) error`
- `sql/schema.sql` and a migration for `task_files` and `suggestions`

### Layer B: Decision Checkpoint on Stop (always-ask)

**Goal**: every Stop forces Claude to explicitly confirm presence/absence of decisions, removing the silent-skip failure mode.

**Mechanism**: extend the `Stop` case in `cmd/endless-hook/claude.go:148`. After `ParseTranscript` and `IdleSession`, return a `Stop` hook response with `decision: "block"` and reason:

> Before stopping, confirm: were any architectural decisions made or refined this turn? If yes, record them with `endless task add --type decision "..."`. If no, reply with `**NO DECISIONS THIS TURN**`.
>
> If you believe this prompt is wrong for this turn pattern, also include:
>
> `**SUGGESTION (decision_checkpoint):** <one-line explanation>`

The post-hook on next UserPromptSubmit parses the prior assistant turn for either the no-decisions confirmation, a recorded decision-task creation, or a suggestion banner. Suggestions land in `suggestions`; non-compliance is recorded but does not re-block (the user has moved on).

**No regex-based decision detection.** Earlier drafts proposed scanning the transcript for phrases like "let's go with." Decisions are semantic, not lexical, and the proxy-by-shape approach fails on the common pattern (user asks → Claude proposes options → user picks → Claude implements). Always-ask is cheaper and more robust.

**Files to modify**:
- `cmd/endless-hook/claude.go:148` (Stop case)
- `cmd/endless-hook/claude.go:118` (UserPromptSubmit, banner parsing shared with Layer A)

### Layer C: SessionEnd Manifest Backstop

**Goal**: catch any edit that slipped past Layers A and B, surface for retroactive recording at the next session.

**Mechanism**: extend the `SessionEnd` case in `cmd/endless-hook/claude.go:158`. The `task_files` table from Layer A already records every edit-time `(task_id, session_id, file_path)`. SessionEnd queries for files this session edited that have no `task_id` association (this should be empty in steady state once Layer A is in place; this is a backstop). If any are found, write a row to a new `pending_audits` table (`session_id`, `project_id`, `files`, `created_at`, `cleared_at NULL`).

At next `SessionStart` (`cmd/endless-hook/claude.go:100`), check `pending_audits WHERE cleared_at IS NULL`. If any exist, inject as `additional_context`:

> Previous session ended with `<N>` unattributed edit(s): `<file list>`. Before continuing, create tasks for these or run `endless audit clear <id>` if they should not be tasked.

**Files to modify**:
- `cmd/endless-hook/claude.go:158` (SessionEnd case)
- `cmd/endless-hook/claude.go:100` (SessionStart, surface pending audits)
- `cmd/endless/audit_cmd.go` (new): `endless audit list`, `endless audit clear <id>`
- Schema: add `pending_audits` table

## Suggestions Capture (cross-cutting, used by all layers)

### Schema

```sql
CREATE TABLE suggestions (
  id          INTEGER PRIMARY KEY,
  session_id  TEXT NOT NULL,
  source      TEXT NOT NULL,           -- 'drift_detection' | 'decision_checkpoint' | future
  trigger_ctx TEXT,                    -- file path, turn excerpt, audit summary
  suggestion  TEXT NOT NULL,           -- Claude's banner text
  created_at  TEXT NOT NULL,
  task_id     INTEGER,                 -- NULL=open, set=accepted into a task
  notes       TEXT,
  FOREIGN KEY (task_id) REFERENCES tasks(id)
);
```

`task_id IS NULL` is the open-suggestion state; `task_id` populated means the suggestion has been accepted and the resulting task lives at `tasks.id = task_id`.

### Banner format (in Claude's response)

```
**SUGGESTION (<source>):** <one-line description of the rule relaxation Claude proposes>
```

The post-hook on next UserPromptSubmit (or Stop, whichever fires first) parses the prior assistant turn for this banner using a fixed regex (the banner format is structured, not the suggestion content). One row per banner.

### CLI

- `endless suggestions list [--source drift_detection|decision_checkpoint] [--all]` — defaults to open suggestions (task_id IS NULL).
- `endless suggestions show <id>`
- `endless suggestions accept <id> [--type chore] [--parent <task-id>]` — runs `endless task add` with the suggestion text as the task body, then sets `suggestions.task_id`. Default type is `chore`.

### Surfacing to Mike

1. **Inline at moment of capture**: Claude's response itself contains the banner, displayed in your terminal naturally. No separate notification needed for in-session visibility.
2. **SessionStart context**: if `count(suggestions WHERE task_id IS NULL) > 0`, inject a one-line reminder. Quiet when none exist.
3. **Tmux alert**: piggy-back on the existing `claude-tmux-alert` global hook (already wired in `~/.claude/settings.json`). When a suggestion lands, fire the alert so the tmux indicator changes.

## Config Knobs (for development friction control)

`.endless/config.json` gains a `checks` map:

```json
{
  "tracking": "enforce",
  "checks": {
    "task_required":       true,
    "drift_detection":     true,
    "decision_checkpoint": true,
    "session_audit":       true
  }
}
```

Each check reads its own flag; defaults to `true` when absent. Disabling one disables only that check, not the broader hook system.

CLI for fast toggling without hand-editing JSON in the heat of debugging:

```
endless config check <name> {on|off}
endless config check                     # list all flags and their current state
```

## Reuse, Don't Rebuild

- `monitor.GetActiveSession(sessionID)` — existing, fetches session and active_task_id.
- `monitor.ParseTranscript(sessionID, path)` — existing, parses Claude transcript JSONL; reuse for banner extraction.
- `monitor.GetTrackingMode(projectID)` — existing, gates all enforcement.
- `hookResponse{AdditionalContext: ...}` and `decision: "block"` patterns — already used at `cmd/endless-hook/claude.go:131` and via `blockToolUse` at `:326`.
- Bash command regex pattern (`taskStartRe` etc., `cmd/endless-hook/claude.go:249`) — extend with a `taskAddDecisionRe` to know when a decision-task was added in this turn.
- `claude-tmux-alert` binary at `/Users/mikeschinkel/.init/bin/claude-tmux-alert` — already wired as global hook; fire from new suggestion landing.

## Verification

End-to-end test scenarios after implementation:

1. **Drift detection (Layer A)**:
   - Start session, `endless task start E-X` (task text mentions only `internal/foo/`).
   - Edit `internal/foo/bar.go` → passes (registered to task_files).
   - Edit `internal/web/baz.go` → blocked, prompt offers switch/sub-task/touch + suggestion banner option.
   - Run `endless task touch E-X --add-file internal/web/baz.go`, retry edit → passes.

2. **Decision checkpoint (Layer B)**:
   - Active session, no architectural discussion this turn. Stop fires; Claude must reply `**NO DECISIONS THIS TURN**` to release.
   - Run another turn that produces a real decision. Claude must run `endless task add --type decision "..."` before Stop releases.

3. **Suggestion capture and accept**:
   - Trigger a drift block with a banner. After turn, `endless suggestions list` shows one open suggestion.
   - `endless suggestions accept <id>` creates a task, sets `suggestions.task_id`, suggestion no longer in default list.

4. **SessionStart surfacing**:
   - Have one open suggestion at session end. Start a new session. SessionStart context mentions the count.

5. **Config knobs**:
   - `endless config check drift_detection off`. Edit a clearly out-of-scope file → no block.
   - Toggle back on → block returns.

6. **SessionEnd backstop (Layer C)**:
   - Force a state where edits land with no `active_task_id` (e.g., flip task_required off temporarily, edit, flip back on, end session).
   - Next SessionStart context lists the unattributed files. `endless audit clear <id>` removes them from the list.

7. **Sanity**:
   - `just build` succeeds. `just install` updates the symlinked `endless-hook`. `just test` for any Python touched.
   - Normal flow (`task start`, in-scope edits, `task complete`) shows no spurious blocks or audit entries.

## Critical Files Reference

| Purpose | Path |
|---|---|
| Hook dispatch | `cmd/endless-hook/claude.go:80` (switch on EventName) |
| PreToolUse block | `cmd/endless-hook/claude.go:256` |
| PostToolUse session detection | `cmd/endless-hook/claude.go:331` |
| UserPromptSubmit handler | `cmd/endless-hook/claude.go:118` |
| Stop handler | `cmd/endless-hook/claude.go:148` |
| SessionEnd handler | `cmd/endless-hook/claude.go:158` |
| SessionStart handler | `cmd/endless-hook/claude.go:100` |
| Tracking mode resolver | `internal/monitor/session.go:305` |
| StartWorkSession | `internal/monitor/session.go:23` |
| Tasks/sessions schema | `sql/schema.sql` |
| CLI task add | `src/endless/cli.py:324` and `src/endless/task_cmd.py` |
| Hook payload struct | `cmd/endless-hook/claude.go` (`claudePayload`) |
| Tmux alert binary | `/Users/mikeschinkel/.init/bin/claude-tmux-alert` |

## Endless Tasks To Create

Parent (type=plan):
- "Enforce task and decision recording via hooks"

Children (each independently shippable in the order listed):

1. (type=feature) "Layer A: Per-task edit-set and drift detection on PreToolUse"
   - sub: schema migration for `task_files`
   - sub: PreToolUse drift logic
   - sub: PostToolUse populates `task_files`
   - sub: `endless task touch --add-file` CLI
2. (type=feature) "Suggestions capture infrastructure"
   - sub: schema migration for `suggestions`
   - sub: banner regex parser in UserPromptSubmit handler
   - sub: `endless suggestions {list,show,accept}` CLI
   - sub: SessionStart unreviewed-count injection
   - sub: tmux alert integration on new suggestion
3. (type=feature) "Layer B: Decision checkpoint on Stop"
   - sub: Stop handler always-ask
   - sub: detection of in-turn decision-task creation
4. (type=feature) "Layer C: SessionEnd manifest backstop"
   - sub: schema migration for `pending_audits`
   - sub: SessionEnd unattributed-files query and write
   - sub: SessionStart surfacing
   - sub: `endless audit {list,clear}` CLI
5. (type=feature) "Config knobs for per-check disable"
   - sub: `.endless/config.json` schema extension
   - sub: each check reads its own flag
   - sub: `endless config check <name> {on|off}` CLI

Suggested implementation order: 1 → 2 → 3 → 4 → 5. Layer A and the suggestions infra are mutually dependent (drift detection produces the first suggestions); ship them together.
