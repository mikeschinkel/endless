# E-971 Layer E — Behavioral Gate (UserPromptSubmit hook)

## Context

Layer D shipped (E-1084) lifecycle-managed worktrees: SessionStart claims/adopts/creates, PreToolUse blocks Write/Edit on worktree↔task mismatch, SessionEnd releases the lock. Mid-session **topic drift** is still unmanaged: a session inside a correctly-bound worktree can still pivot to unrelated work because its active task does not change automatically when the user types "actually let's fix the auth bug." Layer E wires a deterministic gate to that risk.

Two complementary jobs in one new `UserPromptSubmit` flow:

- **Layer 1 — context refresh.** Inject `Active task: E-XXX — <title>.` every prompt. No decision logic. Keeps the active task salient at the moment most likely to drift.
- **Layer 2 — pivot gate.** Substring-match the user prompt against `kind='pivot'` matchers (E-970 already populates them). On match, set a session-scoped gate-pending flag and surface a system reminder. PreToolUse extends to refuse Write/Edit while the gate is pending. The gate clears only via `endless task start <id>`, `endless task confirm <id>`, or `endless task add ...`.

This slice is strictly Layer E. Layer F (auto-create worktree on `task start`) is out of scope.

## Recommended approach

### 1. Add `prompt` to the Claude payload struct

`cmd/endless-hook/claude.go:42-49` — extend `claudePayload` with the field Claude Code sends on `UserPromptSubmit`:

```go
type claudePayload struct {
    SessionID      string          `json:"session_id"`
    CWD            string          `json:"cwd"`
    EventName      string          `json:"hook_event_name"`
    ToolName       string          `json:"tool_name,omitempty"`
    ToolInput      json.RawMessage `json:"tool_input,omitempty"`
    TranscriptPath string          `json:"transcript_path,omitempty"`
    Prompt         string          `json:"prompt,omitempty"` // NEW
}
```

The Anthropic Claude Code hook spec ships `prompt` directly in the payload for `UserPromptSubmit`. No transcript-tail parsing required.

### 2. Schema migration `migrateV5` — `session_gates` table

A separate table keeps gate triggers as first-class telemetry: every pivot match records a row with the phrase, when it triggered, when it cleared, and which CLI verb cleared it. Useful for "how often does Claude pivot, on which phrases, and how do users typically resolve it?"

Schema:

```sql
CREATE TABLE session_gates (
    id INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    matcher_phrase TEXT NOT NULL,
    triggered_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    cleared_at TEXT,
    cleared_by TEXT,                  -- 'task_start' | 'task_confirm' | 'task_add' | 'superseded'
    FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
);
CREATE INDEX idx_session_gates_session ON session_gates(session_id, triggered_at DESC);
```

A gate is "open" when `cleared_at IS NULL`. A session has at most one open gate at a time; if a new pivot fires while one is open, the prior row is marked `cleared_by='superseded'` and a new row inserted (preserves both events for telemetry, but only one is open).

Add to `internal/monitor/db.go:140-146`:

```go
func migrate(db *sql.DB) {
    BackupDB()
    migrateV1(db); migrateV2(db); migrateV3(db); migrateV4(db)
    migrateV5(db)
}

func migrateV5(db *sql.DB) {
    if !hasTable(db, "session_gates") {
        db.Exec(`CREATE TABLE IF NOT EXISTS session_gates (
            id INTEGER PRIMARY KEY,
            session_id TEXT NOT NULL,
            matcher_phrase TEXT NOT NULL,
            triggered_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
            cleared_at TEXT,
            cleared_by TEXT,
            FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
        )`)
        db.Exec(`CREATE INDEX IF NOT EXISTS idx_session_gates_session
            ON session_gates(session_id, triggered_at DESC)`)
    }
}
```

### 3. Gate helpers — new file `internal/monitor/session_gate.go`

Keep gate concerns out of `session.go` since gates have their own table. New file with three helpers:

```go
// SetGatePending opens a new gate row for the session. If an open gate
// already exists, mark it superseded before inserting the new one.
func SetGatePending(sessionID, phrase string) error {
    db, err := DB()
    if err != nil { return err }
    now := time.Now().UTC().Format("2006-01-02T15:04:05")
    if _, err = db.Exec(
        `UPDATE session_gates SET cleared_at=?, cleared_by='superseded'
         WHERE session_id=? AND cleared_at IS NULL`,
        now, sessionID); err != nil {
        return err
    }
    _, err = db.Exec(
        `INSERT INTO session_gates (session_id, matcher_phrase, triggered_at)
         VALUES (?, ?, ?)`,
        sessionID, phrase, now)
    return err
}

// ClearGatePending marks all open gates for the session as cleared by
// the given verb (e.g., "task_start", "task_confirm", "task_add").
func ClearGatePending(sessionID, clearedBy string) error {
    db, err := DB()
    if err != nil { return err }
    now := time.Now().UTC().Format("2006-01-02T15:04:05")
    _, err = db.Exec(
        `UPDATE session_gates SET cleared_at=?, cleared_by=?
         WHERE session_id=? AND cleared_at IS NULL`,
        now, clearedBy, sessionID)
    return err
}

// IsGatePending returns (true, phrase) if the session has an open gate.
// Phrase is the matcher of the most recent open gate.
func IsGatePending(sessionID string) (bool, string) {
    db, err := DB()
    if err != nil { return false, "" }
    var phrase string
    err = db.QueryRow(
        `SELECT matcher_phrase FROM session_gates
         WHERE session_id=? AND cleared_at IS NULL
         ORDER BY triggered_at DESC LIMIT 1`,
        sessionID).Scan(&phrase)
    if err != nil { return false, "" }
    return true, phrase
}
```

`SessionInfo` and `GetActiveSession` are unchanged — gate state lives entirely in the new table.

### 4. Pivot detection in `internal/matchers/matchers.go`

Add two helpers below `ActionRegex` (after line 137):

```go
// GetPivotMatchers returns enabled matchers with type="pivot".
func GetPivotMatchers(all []Matcher) []Matcher {
    out := make([]Matcher, 0)
    for _, m := range all {
        if m.Type == "pivot" && m.IsEnabled() {
            out = append(out, m)
        }
    }
    return out
}

// FindPivotMatch returns the first phrase from any pivot matcher that
// substring-matches text. Empty string means no match. Honors
// case_sensitive per matcher (default: case-insensitive substring).
func FindPivotMatch(all []Matcher, text string) string {
    lower := strings.ToLower(text)
    for _, m := range GetPivotMatchers(all) {
        if m.Method != "substring" && m.Method != "exact" {
            continue
        }
        for _, phrase := range m.MatchList() {
            if m.CaseSensitive {
                if strings.Contains(text, phrase) { return phrase }
            } else {
                if strings.Contains(lower, strings.ToLower(phrase)) { return phrase }
            }
        }
    }
    return ""
}
```

Live config already has the matchers (`~/.config/endless/config.json` ships `pivot` substring entries plus the case-sensitive `PIVOT` override). No config changes for pivot detection.

### 5. UserPromptSubmit handler — Layer 1 + Layer 2

Refactor the existing `handleTaskContextInjection` (line 217) to **return the context string** instead of writing the response itself (caller does the encode). Then rewrite the `UserPromptSubmit` case (line 140) to compose one response:

```go
case "UserPromptSubmit":
    monitor.ParseTranscript(payload.SessionID, payload.TranscriptPath)
    if err := monitor.ScanRecentSuggestions(payload.SessionID, projectID); err != nil {
        log.Printf("scanning suggestions: %v", err)
    }
    if err := monitor.BackfillProcess(payload.SessionID, os.Getenv("TMUX_PANE")); err != nil {
        return fmt.Errorf("backfilling process: %w", err)
    }
    if err := writeClaudeCompanion(projectID, payload); err != nil {
        return fmt.Errorf("refreshing companion file: %w", err)
    }

    var parts []string

    // Pending inter-session messages (existing fallback)
    pane := os.Getenv("TMUX_PANE")
    if port, _, _ := monitor.LookupChannelPort(pane); port == 0 {
        if hasMsgs, _ := monitor.HasPendingMessages(pane); hasMsgs {
            parts = append(parts, "You have pending inter-session messages. Run: endless channel inbox")
        }
    }

    // First-time full task injection (one-shot per session) OR Layer 1 single-line reminder
    session, _ := monitor.GetActiveSession(payload.SessionID)
    if !monitor.HasInjectedContext(payload.SessionID) {
        if ctx, err := buildTaskContextInjection(projectID, payload); err == nil && ctx != "" {
            parts = append(parts, ctx)
        }
    } else if session != nil && session.ActiveTaskID != nil {
        if title, err := monitor.GetTaskTitle(*session.ActiveTaskID); err == nil {
            parts = append(parts, fmt.Sprintf("Active task: E-%d — %s.", *session.ActiveTaskID, title))
        }
    }

    // Layer 2: pivot detection
    if payload.Prompt != "" {
        all, _ := matchers.Load(projectID)
        if matched := matchers.FindPivotMatch(all, payload.Prompt); matched != "" {
            if err := monitor.SetGatePending(payload.SessionID, matched); err != nil {
                log.Printf("setting gate-pending: %v", err)
            }
            parts = append(parts, fmt.Sprintf(
                "Pivot trigger detected (matched: %q). Confirm or update active task before any Write/Edit. "+
                    "Cleared by `endless task add ...` (new task), `endless task start <id>` (continue/switch), "+
                    "or `endless task confirm <id>` (continue same).", matched))
        }
    }

    if len(parts) == 0 {
        return nil
    }
    return json.NewEncoder(os.Stdout).Encode(hookResponse{
        AdditionalContext: strings.Join(parts, "\n\n"),
    })
```

`buildTaskContextInjection` is `handleTaskContextInjection` refactored to return `(string, error)` and call `MarkContextInjected` inline; the existing `SessionStart` caller (line 138) gets a thin wrapper that encodes the result. (Or simply leave SessionStart on the existing function, and have `buildTaskContextInjection` be a private helper invoked by both paths.)

### 6. PreToolUse — refuse Write/Edit when gate is pending

`cmd/endless-hook/claude.go:362-420`. Add the gate check right after `enforceWorktreeGate` (line 384) and before the tracking-mode short-circuit. This is independent of `tracking_mode` — like E-1012 and Layer D, the pivot gate must fire even when per-task tracking is `off`:

```go
enforceWorktreeGate(projectID, payload)

// Pivot gate (E-971 Layer E)
if pending, phrase := monitor.IsGatePending(payload.SessionID); pending {
    blockToolUse(fmt.Sprintf(
        "BLOCKED (pivot gate): your last user message contained %q.\n\n"+
        "Edits are paused until you declare your task. Run one of:\n"+
        "  endless task add \"<title>\"      (new task)\n"+
        "  endless task start E-NNN          (switch to / resume an existing task)\n"+
        "  endless task confirm E-NNN        (continue with / wrap up the current task)",
        phrase))
}

mode := monitor.GetTrackingMode(projectID)
```

### 7. PostToolUse — rename `complete` to `confirm`, add `add`, clear gate on all three

`handlePostToolUseSession` (line 481) already detects `endless task start` (line 498) and `endless task complete` (line 517) via matchers. Three changes:

**(a) Rename the existing `complete` matcher to `confirm`.** The user-facing CLI verb is `endless task confirm` (registered at `cli.py:767`); `complete` is only the underlying Python function name. The current matcher `complete` matches no real Bash command Claude would ever type. Replace it outright in `~/.config/endless/config.json`:

```json
{
  "type": "confirm",
  "scope": "task",
  "method": "regex",
  "match": "endless\\s+task\\s+confirm\\s+(?:[Ee]-)?(\\d+)"
}
```

In `claude.go:352` rename the constant and call site:

```go
const actionConfirm = "confirm"  // was actionComplete = "complete"
// ...
if re := matchers.ActionRegex(all, actionConfirm, scopeTask); re != nil { ... }
```

This is a clean replace — no aliasing, no dual-match regex. As a side effect it fixes a pre-existing latent bug where `task confirm` ran but the hook's `monitor.CompleteTask` (which is the only thing in the codebase that clears `sessions.active_task_id` on task completion) silently never fired.

**(b) Add an `add` action regex.** Append to `~/.config/endless/config.json`:

```json
{
  "type": "add",
  "scope": "task",
  "method": "regex",
  "match": "endless\\s+task\\s+add\\b"
}
```

Add `actionAdd = "add"` to the constants block at line 350.

**(c) Wire gate clearance at all three points** in `handlePostToolUseSession`. The `clearedBy` argument is recorded on the gate row for telemetry:

```go
// After StartWorkSession success (line 504):
if err := monitor.ClearGatePending(payload.SessionID, "task_start"); err != nil {
    log.Printf("clearing gate after task start: %v", err)
}

// After CompleteTask success (line 522):
if err := monitor.ClearGatePending(payload.SessionID, "task_confirm"); err != nil {
    log.Printf("clearing gate after task confirm: %v", err)
}

// New block: detect endless task add (after the chat block, before channel verbs)
if re := matchers.ActionRegex(all, actionAdd, scopeTask); re != nil && re.MatchString(input.Command) {
    if err := monitor.ClearGatePending(payload.SessionID, "task_add"); err != nil {
        log.Printf("clearing gate after task add: %v", err)
    }
    return nil
}
```

### 8. SessionEnd — no change required

When the session row is updated to `state='ended'` in `EndSession`, `gate_pending_phrase` carries forward as the last value, but no future PreToolUse will run against an ended session. We don't bother nulling it.

## Critical files

- `cmd/endless-hook/claude.go` — payload struct (line 42), UserPromptSubmit handler (line 140), PreToolUse gate check (line 384), PostToolUse rename `actionComplete` → `actionConfirm` and add `actionAdd` (line 350-360, 481), refactor `handleTaskContextInjection` (line 217)
- `internal/monitor/db.go` — `migrate` (line 140), new `migrateV5` creating `session_gates`
- `internal/monitor/session_gate.go` (new) — `SetGatePending` / `ClearGatePending` / `IsGatePending`
- `internal/matchers/matchers.go` — new `GetPivotMatchers` / `FindPivotMatch` (after line 137)
- `~/.config/endless/config.json` — replace `complete` matcher with `confirm`; add `add` matcher

## Reused functions / utilities

- `monitor.GetActiveSession` (`internal/monitor/session.go:96`) — extend SELECT to include gate column
- `monitor.GetTaskTitle` — for Layer 1 single-line reminder
- `monitor.HasInjectedContext` / `MarkContextInjected` — first-time vs subsequent injection branching
- `matchers.Load` / `matchers.ActionRegex` (`internal/matchers/matchers.go:68, 115`) — same loader for the new `add` matcher
- `blockToolUse` (`cmd/endless-hook/claude.go:457`) — exit-code-2 stderr block, used for gate refusal

## Verification

End-to-end exercise the gate using the matchers already in `~/.config/endless/config.json` (with the new `confirm` and `add` rows added). From a worktree session:

1. **Layer 1 baseline.** Start a session inside an active task's worktree. Send a benign prompt ("show me the README"). Confirm Claude received `Active task: E-XXX — <title>.` in its system context (visible via the hook log at `~/.config/endless/log/hook.log`).
2. **Pivot — case-insensitive.** Type "actually, let's look at auth instead". Verify:
   - Hook log shows the Layer 2 trigger.
   - `sqlite3 ~/.config/endless/endless.db "SELECT session_id, matcher_phrase, triggered_at, cleared_at FROM session_gates WHERE session_id=?"` shows one open row with `matcher_phrase='actually'` and `cleared_at IS NULL`.
   - Asking Claude to Write/Edit returns the gate-block message echoing `actually`; no file changes occur.
3. **Pivot — capital-A.** Type "Actually, let me check the logs". Same outcome (case-insensitive matcher fires regardless of case).
4. **Pivot — `PIVOT` override.** Type "PIVOT to db schema work". Same outcome — and a second open row appears with the prior row marked `cleared_by='superseded'`.
5. **Disable + retry.** Run `endless phrase disable pivot actually`. Re-trigger with "actually …". No new open row inserted. The case-sensitive `PIVOT` matcher still fires.
6. **Clearance via `task start`.** With a pending gate, run `endless task start E-NNN`. Open row's `cleared_at` populates and `cleared_by='task_start'`. Subsequent Write/Edit succeed.
7. **Clearance via `task confirm`.** Set the gate, run `endless task confirm E-NNN`. Open row clears with `cleared_by='task_confirm'`. As a bonus, verify `sessions.active_task_id` was nulled (the latent bug fix from §7a).
8. **Clearance via `task add`.** Set the gate, run `endless task add "Fix auth flow" --type chore --phase later`. Open row clears with `cleared_by='task_add'`. Verify the Bash hook log shows the `add` matcher detected.
9. **Fresh session unaffected.** Start a new session in the same project. No open `session_gates` rows for the new session_id (gate is per-session).
10. **Telemetry sanity.** After running the above, `SELECT cleared_by, count(*) FROM session_gates GROUP BY cleared_by` returns a populated breakdown — that's the artifact we got from putting gates in their own table.

## Discipline

- Work in a worktree. After `git worktree add`, run inside the worktree:
  ```sh
  just go-work-init           # generates go.work with absolute paths to ../go-pkgs
  just claude-settings-init   # writes <worktree>/.claude/settings.json so Claude in this worktree calls <worktree>/bin/endless-hook
  just build                  # produces bin/endless-hook for the override above to invoke
  ```
- Do **not** run `just install` from the worktree (per memory: hijacks the global tool path). Test the Python CLI with `PYTHONPATH=<worktree>/src endless ...`.
- The hook override means Claude sessions whose cwd is inside the worktree exercise *this* `bin/endless-hook` automatically, while sessions elsewhere keep using the global symlink. So the verification steps above can run in a Claude session inside the worktree without touching anything global.
- For unit-style checks of the hook binary, also fine to call `./bin/endless-hook claude` directly with crafted JSON on stdin (fast, deterministic, no Claude required).
- Ship via `endless worktree land <task-id>`. Refresh the toolchain from main with `just install` once landed.

## What this slice does NOT do

- **No Layer F.** No auto-create worktree on `task start` for plan-bearing tasks.
- **No new pivot UX.** The gate sets a flag and surfaces a system reminder. It does **not** propose a draft title for `task add` or suggest target tasks for `task start`. Mike will see whether the deterministic block is sufficient before adding any agent-side smarts.
- **No TTL on the gate.** Cleared only by the three CLI verbs. (If the user abandons mid-pivot, gate dies with the session at SessionEnd.)
