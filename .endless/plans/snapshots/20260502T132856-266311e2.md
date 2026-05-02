# E-971 Layer D — Worktree adoption hooks

## Context

E-971 ("worktrees and the behavioral gate") gives every editing session its own git worktree so parallel Claude sessions stop colliding on shared files. Foundation slice (`endless worktree {list, current, show, for-task}`) and mutation slice (`land`, `drop`) are already in main. What's missing is the runtime that *binds* sessions to worktrees — without it, sessions still launch in main, edit there, and the per-session isolation never happens.

This slice is **Layer D only**: hook-level adoption and enforcement, plus the lock primitives that make ownership transfer safe. Layer E (UserPromptSubmit gate) and Layer F (`endless pivot` and worktree creation on task start) are out of scope and ship separately.

### How Layer D fits with Layer F (the actual UX target)

The dominant user flow is mid-session evolution: a session starts as exploration (no task), turns into a plan, turns into implementation (now there's a task). The user must not have to /quit Claude and lose context to make that transition. That's the hard problem this design solves.

The mechanism (path redirection — option C from the design discussion) is split between two slices:

- **Layer D (this slice)** enforces the rules: no edits in main, no edits across sessions sharing a worktree, no edits to files outside the active task's worktree.
- **Layer F (future)** creates worktrees and emits redirection guidance: when the user invokes `endless pivot` or `endless task start` from inside Claude, the hook creates the worktree, sets the active task, and returns a context message: *"active task is now E-XXX, worktree at `<path>`; run `cd <path>` for Bash and use that prefix for file paths."* Claude follows the redirection. Layer D's PreToolUse blocks any non-compliance.

Layer D shipped alone is a strict gate: it surfaces the seam but doesn't fix it. Layer D + Layer F together implement the seamless pivot. The plan is to ship D first (it's smaller, self-contained, and earns the safety guarantees) and then F.

### Why Claude's cwd matters at all

Filesystem isolation requires Claude's tool calls to reference the worktree's absolute paths, not main's. There are only two ways to make that happen:

1. **Claude's process cwd is in the worktree** (cold start via wrapper, or user manually cd'd before launching).
2. **Claude knows to use absolute paths into the worktree** even though its process cwd is elsewhere (option C: hook tells Claude where to write; Bash gets a one-time `cd <worktree>` whose effect persists across subsequent Bash calls per Claude Code's behavior).

A hook cannot change Claude's cwd directly — cwd is a per-process attribute and the hook is a child process. So mid-session primary change, where /quit is unacceptable, has to use option C.

## Scope

In scope:

1. SessionStart hook: walk-up + adoption (claim unowned, refuse owned, idempotent re-entry). **No auto-creation.**
2. PreToolUse hook: extend the existing E-1012/E-917 enforcement with worktree-aware blocks for Write/Edit/NotebookEdit:
   - Refuse edits when cwd is in main.
   - Refuse edits when cwd's worktree is owned by a different session.
   - Refuse edits when cwd's worktree's task does not match the session's active task.
   - Refuse edits when the session has an active task whose worktree exists but cwd is not in it (Layer F's redirection message will tell Claude where to go; Layer D's job is just to refuse non-compliance).
3. SessionEnd hook: release any worktree lock claimed by this session.
4. Lock-file primitives in `internal/monitor/worktree_lock.go`: claim, read, stale-check, release, find-by-session-id.
5. Walk-up helper: find `.endless/worktree.json` from a cwd, bounded at the registered project root.
6. Companion-file reader for the worktree.json shape (parses `kind`, `task_id`, `session_id`, `base_branch`, `branch`, `created_at`).

Out of scope (explicitly):

- **Auto-creating worktrees in SessionStart.** Removed from the original brief — bootstrap UX cost was too high. Worktree creation belongs to `endless pivot` (Layer F).
- UserPromptSubmit phrase-gate (E-971 Layer E).
- `endless pivot` verb design and `endless task start` redirection messages (Layer F).
- `endless task spawn` worktree-awareness (Layer F or its own slice; spawn is the building block, pivot is the high-level UX).
- Any change to `endless worktree land/drop`.
- DB schema changes — worktree state remains filesystem-authoritative.

## Companion-file format

Lives at `<worktree-root>/.endless/worktree.json`. Existing Python code (`src/endless/worktree_cmd.py:42-127`) reads it; only `task_id` is required for landing.

**On the term `kind` (replacing `flavor`)**: per design discussion, `kind` is the better term — it's the K8s-standard name for a discriminated-union discriminator, it's short, it's neutral. The parent E-971.md doc uses `flavor` and will need to be updated for consistency before Layer F starts. That update is a small co-change scheduled with this slice's PR (one-line change in E-971.md to reflect the rename).

**Task kind** (written by future Layer F):
```json
{
  "kind": "task",
  "task_id": "E-808",
  "base_branch": "main",
  "branch": "task/808-event-logs-authoritative",
  "created_at": "2026-05-02T15:30:00Z"
}
```

**Session kind** (Layer D doesn't write either — included for shape parity. If/when a session-without-task wrapper or Layer F creates these, the format is):
```json
{
  "kind": "session",
  "session_id": "<harness session uuid>",
  "base_branch": "main",
  "branch": "session/<short-id>",
  "created_at": "2026-05-02T15:30:00Z"
}
```

Layer D reads both kinds via the same Go struct; readers tolerate missing fields per kind.

## Lock-file primitives — `internal/monitor/worktree_lock.go` (NEW)

Mirrors the style of `internal/monitor/companion.go` (root-relative paths, atomic writes). Separate file keeps the worktree-ownership concern from bloating `companion.go`.

**Lock path:** `<worktree-root>/.endless/worktree.lock`

**Lock contents (JSON):**
```json
{
  "session_id": "<harness session uuid>",
  "pid": 12345,
  "tmux_pane": "%17",
  "claimed_at": "2026-05-02T15:30:00Z"
}
```

**API:**

```go
type WorktreeLock struct {
    SessionID string `json:"session_id"`
    PID       int    `json:"pid"`
    TmuxPane  string `json:"tmux_pane,omitempty"`
    ClaimedAt string `json:"claimed_at"`
}

type WorktreeCompanion struct {
    Kind       string `json:"kind"`
    TaskID     string `json:"task_id,omitempty"`
    SessionID  string `json:"session_id,omitempty"`
    BaseBranch string `json:"base_branch,omitempty"`
    Branch     string `json:"branch,omitempty"`
    CreatedAt  string `json:"created_at,omitempty"`
}

// ReadWorktreeCompanion reads <worktree>/.endless/worktree.json.
// Returns nil, os.ErrNotExist if absent.
func ReadWorktreeCompanion(worktreePath string) (*WorktreeCompanion, error)

// ClaimWorktreeLock atomically creates the lock file. Returns
// os.ErrExist if a lock already exists. Caller checks for staleness
// via ReadWorktreeLock + IsWorktreeLockStale before retrying.
func ClaimWorktreeLock(worktreePath string, lock WorktreeLock) error

// ReadWorktreeLock reads and parses the lock file. Returns nil,
// os.ErrNotExist if absent.
func ReadWorktreeLock(worktreePath string) (*WorktreeLock, error)

// IsWorktreeLockStale returns true if lock.PID is no longer alive.
func IsWorktreeLockStale(lock *WorktreeLock) bool

// ReleaseWorktreeLock deletes the lock file. Idempotent.
func ReleaseWorktreeLock(worktreePath string) error

// FindLockBySessionID scans <project_root>/.endless/worktrees/*/.endless/worktree.lock
// and returns the worktree path whose lock has the given session_id, or
// "" if none. Used by SessionEnd.
func FindLockBySessionID(projectID int64, sessionID string) (string, error)

// FindWorktreeRoot walks up from cwd looking for .endless/worktree.json.
// Stops at projectRoot (inclusive — does not walk above it). Returns the
// worktree directory (the dir containing .endless/worktree.json), or "" if
// not found.
func FindWorktreeRoot(cwd, projectRoot string) (string, error)
```

**Atomic claim:** `os.OpenFile(path, O_WRONLY|O_CREATE|O_EXCL, 0644)`. POSIX guarantees `O_EXCL` is atomic across processes. On `EEXIST`, return `os.ErrExist`.

**Stale check — `syscall.Kill(pid, 0)`.** Despite the name, this does **not** kill the process. The Unix `kill(2)` syscall has a special-case: signal **0** is a no-op signal — the kernel runs all the existence/permission checks but never delivers anything to the target. So `syscall.Kill(pid, 0)` is the canonical "is this PID alive?" probe:

- returns `nil` → process exists and is signalable by us (alive)
- returns `syscall.ESRCH` → no such process (dead — lock is stale)
- returns `syscall.EPERM` → exists but owned by another user (alive — do NOT reclaim)
- any other error → treat as "uncertain — alive" to err on not stealing locks

The Go function is named `Kill` because the underlying syscall is named `kill`, not because signal 0 kills anything. See `man 2 kill` ("If sig is 0, then no signal is sent, but existence and permission checks are still performed"). Add a comment explaining the idiom so future readers don't second-guess it.

**Platform note:** endless depends on tmux (macOS/Linux only) for several core features, so Windows support is deferred broadly. The signal-0 idiom is Unix; on Windows we'd need `OpenProcess` instead. Out of scope for this slice; will follow whatever decision endless makes about Windows support more broadly.

**TOCTOU on stale-lock reclaim:** between "lock looks stale" and "I overwrote it" there is a window where two sessions could race. We accept this — the worst case is two sessions briefly think they own the same worktree, and the next PreToolUse will catch it via session-id mismatch. v1 doesn't need flock-based fencing.

## Walk-up helper

```go
func FindWorktreeRoot(cwd, projectRoot string) (string, error)
```

Implementation: `filepath.Clean(cwd)`; loop `Stat(filepath.Join(dir, ".endless", "worktree.json"))`; if found, return dir; otherwise `dir = filepath.Dir(dir)`; stop when `dir == projectRoot` or `dir == filepath.Dir(dir)` (filesystem root).

The walk-up tells you whether there's a worktree companion. Use the existing `isInMainCheckout(cwd)` (`cmd/endless-hook/claude.go:623`) to tell you which side of the main/worktree boundary you're on. Both are needed — they answer different questions.

## SessionStart extension — `cmd/endless-hook/claude.go:104-127`

Terminology: `payload` is the parsed JSON Claude sends to the hook on stdin (struct at `claude.go:41-48`). `payload.CWD` is the `cwd` field — the working directory of the Claude process when the hook fired.

After the existing block (InitSession, SetProcess, transcript path, tmuxTaskID, writeClaudeCompanion) and before `handleTaskContextInjection`, insert:

```
1. projectRoot := monitor.ProjectPath(projectID)
2. worktreePath := monitor.FindWorktreeRoot(payload.CWD, projectRoot)
3. branch by case:
   case A: worktreePath != "" (cwd is inside an endless-managed worktree)
       lock, err := monitor.ReadWorktreeLock(worktreePath)
       if err == os.ErrNotExist:
           ClaimWorktreeLock(...); proceed
       else if err == nil:
           if IsWorktreeLockStale(lock):
               ReleaseWorktreeLock(worktreePath); ClaimWorktreeLock(...); proceed
           else if lock.SessionID == payload.SessionID:
               // idempotent re-entry — no-op
               proceed
           else:
               return additionalContext refusal:
                 "This worktree is owned by session <id> (PID <n>).
                  Open a new shell in a different worktree (or in main)
                  and start a Claude session there."
               // PreToolUse will subsequently block edits because the
               // lock-owner check fails.
   case B: worktreePath == ""
       // cwd is either in main or in a foreign tree (or somewhere
       // unrelated). Do nothing. PreToolUse will block edits if the
       // user attempts them and provide a helpful message.
       proceed
4. Continue with handleTaskContextInjection as before.
```

**Idempotency:** Re-entries (Claude resumes) hit case A with `lock.SessionID == payload.SessionID` and short-circuit. SessionStart can fire more than once per Claude UUID across resumes.

**No auto-create:** the original brief had a case-B that did `git worktree add`. Removed because the bootstrap UX (user must /quit, cd, restart) violates the "no context loss" goal that motivates this entire design. Worktree creation moves to `endless pivot` and `endless task start` extensions in Layer F, which can emit redirection context that lets Claude continue in the same conversation.

## PreToolUse extension — `cmd/endless-hook/claude.go:333`

Insert worktree-aware blocks **before** the existing writeTools enforcement (line 348) and **after** the existing E-1012 git-commit block (lines 339-345):

```go
// Worktree gate (E-971 Layer D). Independent of tracking_mode, like E-1012.
if writeTools[payload.ToolName] {
    projectRoot, _ := monitor.ProjectPath(projectID)
    worktreePath, _ := monitor.FindWorktreeRoot(payload.CWD, projectRoot)
    session, _ := monitor.GetActiveSession(payload.SessionID)

    if worktreePath == "" {
        // cwd has no worktree companion. Two sub-cases.
        if inMain, _ := isInMainCheckout(payload.CWD); inMain {
            // Look up: does the session have an active task with a worktree?
            var redirectHint string
            if session != nil && session.ActiveTaskID != nil {
                if wp, _ := monitor.WorktreePathForTask(projectID, *session.ActiveTaskID); wp != "" {
                    redirectHint = fmt.Sprintf(
                        "\n\nYour active task E-%d has a worktree at:\n  %s\n\n"+
                        "Run `cd %s` in a Bash call (the new cwd persists for "+
                        "subsequent Bash calls), and use absolute paths under "+
                        "that directory for Read/Write/Edit.",
                        *session.ActiveTaskID, wp, wp)
                }
            }
            blockToolUse(`Edits in main are not allowed (E-971).

main is the integration target — every edit goes through a worktree.

If you do not yet have an active task, create one and start it:
  endless task add "<title>"
  endless task start E-NNN

If you have an active task without a worktree, create the worktree
  via `endless pivot` (when available) or by hand:
  git worktree add -b task/NNN-<slug> .endless/worktrees/e-NNN main` + redirectHint)
        }
        // Foreign worktree (no endless companion) — leave alone, fall
        // through to existing checks. Foreign trees are listed-only.
    } else {
        // We are inside an endless-managed worktree. Three checks.

        // (a) Lock-owner check.
        lock, lockErr := monitor.ReadWorktreeLock(worktreePath)
        if lockErr == nil && lock != nil && lock.SessionID != payload.SessionID {
            ownerHint := fmt.Sprintf("session %s (PID %d)", lock.SessionID, lock.PID)
            if monitor.IsWorktreeLockStale(lock) {
                ownerHint += " [stale]"
            }
            blockToolUse(fmt.Sprintf(`This worktree is owned by %s, not this session.

Restart this Claude session inside this worktree (a fresh SessionStart
reclaims a stale lock), or move to a different worktree.

  endless worktree current
  endless worktree list`, ownerHint))
        }

        // (b) Task mismatch: worktree's task != session's active task.
        comp, _ := monitor.ReadWorktreeCompanion(worktreePath)
        if comp != nil && comp.TaskID != "" {
            if session != nil && session.ActiveTaskID != nil {
                worktreeTaskNum, parseErr := parseEndlessTaskID(comp.TaskID)
                if parseErr == nil && worktreeTaskNum != *session.ActiveTaskID {
                    blockToolUse(fmt.Sprintf(
                        "This worktree is bound to %s, but your active task is E-%d.\n\n"+
                        "Either switch tasks (no cd needed):\n"+
                        "  endless task start E-%d\n\n"+
                        "Or move to the worktree for your active task:\n"+
                        "  endless worktree for-task E-%d",
                        comp.TaskID, *session.ActiveTaskID,
                        worktreeTaskNum, *session.ActiveTaskID))
                }
            }
        }

        // (c) Session has an active task whose worktree exists, but cwd is
        // not in it. (e.g. session is in worktree A, but `endless task start`
        // was called for task B which has its own worktree.) Layer F's
        // redirection message will guide Claude; here we just block.
        if session != nil && session.ActiveTaskID != nil {
            activeWP, _ := monitor.WorktreePathForTask(projectID, *session.ActiveTaskID)
            if activeWP != "" && activeWP != worktreePath {
                blockToolUse(fmt.Sprintf(
                    "Your active task E-%d is bound to a different worktree:\n  %s\n\n"+
                    "Use absolute paths under that directory, and run "+
                    "`cd %s` in a Bash call for shell commands.",
                    *session.ActiveTaskID, activeWP, activeWP))
            }
        }
    }
}
// ...existing writeTools enforcement continues below
```

**Reuses:**
- `isInMainCheckout` (claude.go:623), `runGitRevParse` (claude.go:646)
- `monitor.GetActiveSession` (session.go:96)
- `monitor.WorktreePathForTask` (companion.go:70)
- `writeTools` (claude.go:296), `blockToolUse` (claude.go:423)

**Adds:**
- `monitor.ReadWorktreeCompanion`, `monitor.ReadWorktreeLock`, `monitor.IsWorktreeLockStale`, `monitor.FindWorktreeRoot` — all in the new `worktree_lock.go`.
- `parseEndlessTaskID(s string) (int64, error)` — local helper in claude.go; parses `"E-808"` → `808`.

**On the lock-owner check (per Mike's call):** SessionStart's refusal is informational; PreToolUse enforcement makes it binding. If this turns out too strict in practice, we backtrack and remove.

## SessionEnd extension — `cmd/endless-hook/claude.go:180-191`

After `RemoveCompanion` and before `EndSession`, release any worktree lock owned by this session. Use `FindLockBySessionID` rather than walk-up-from-cwd, because the user may have cd'd out before quitting.

```go
case "SessionEnd":
    monitor.ParseTranscript(payload.SessionID, payload.TranscriptPath)
    monitor.FlagNeedsRecap(payload.SessionID)
    if err := monitor.RemoveCompanion(projectID, "claude", payload.SessionID); err != nil {
        return fmt.Errorf("removing companion file: %w", err)
    }
    if wtPath, err := monitor.FindLockBySessionID(projectID, payload.SessionID); err == nil && wtPath != "" {
        if err := monitor.ReleaseWorktreeLock(wtPath); err != nil {
            log.Printf("releasing worktree lock at %s: %v", wtPath, err)
            // Non-fatal: stale-PID check on next claim attempt will reap it.
        }
    }
    if err := monitor.EndSession(payload.SessionID); err != nil {
        return fmt.Errorf("ending session: %w", err)
    }
```

Worktree itself is **not** removed at SessionEnd — only the lock. Worktrees persist until `endless worktree land/drop`.

## Critical files

| File | Action |
|---|---|
| `internal/monitor/worktree_lock.go` | NEW — lock primitives + walk-up + companion reader |
| `cmd/endless-hook/claude.go` | EDIT — SessionStart (after line 126), PreToolUse (before line 348), SessionEnd (after line 187) |
| `.endless/plans/E-971.md` | EDIT — one-line `flavor` → `kind` rename for terminology consistency before Layer F starts |

## What works after Layer D ships alone (without Layer F)

- Edits in main are blocked (safety win even alone — prevents accidental main edits).
- Sessions started inside an existing worktree adopt cleanly; no two sessions can edit the same worktree simultaneously.
- Stale locks are reclaimable.
- Task-worktree mismatches are caught.

## What needs Layer F to be smooth

- The redirection messages in PreToolUse refusals describe what to do; **emitting those messages from `endless pivot` / `endless task start` proactively** (so Claude follows them before being blocked) is Layer F.
- Worktree creation on first task association is Layer F.
- The "exploration → implementation" mid-session pivot UX is the Layer D + Layer F combination.

This split is intentional: Layer D earns the safety guarantees standalone; Layer F earns the UX polish. Shipping D first is a strict gate that nudges users toward the right pattern (and emits enough breadcrumbs in the block messages that a savvy user can comply manually).

## Reused helpers (do not duplicate)

- `monitor.ProjectPath(projectID)` — `internal/monitor/db.go:466`
- `monitor.GetActiveSession(sessionID)` — `internal/monitor/session.go:96`
- `monitor.WorktreePathForTask(projectID, taskID)` — `internal/monitor/companion.go:70`
- `monitor.WriteCompanion` atomic-tempfile pattern — model the lock + companion writes on `companion.go:98-136`
- `isInMainCheckout` + `runGitRevParse` — `cmd/endless-hook/claude.go:623, 646`
- `blockToolUse` — `cmd/endless-hook/claude.go:423`
- `writeTools` map — `cmd/endless-hook/claude.go:296`

## Decisions / tradeoffs locked

1. **`kind` not `flavor`** — terminology aligned with K8s convention; one-line update to E-971.md ships in this slice's PR.
2. **No auto-create at SessionStart** — bootstrap UX cost too high; creation moves to Layer F via `endless pivot`.
3. **Lock release uses session-id scan, not cwd walk-up** — handles the case where the user cd'd out before /quit.
4. **PreToolUse worktree-on-main edit block is independent of `tracking_mode`**, matching E-1012's pattern. Same for the lock-owner check and the active-task-elsewhere check.
5. **Lock-owner check at PreToolUse** (Mike's call) — belt-and-suspenders to make the SessionStart refusal binding.
6. **TOCTOU on stale-lock reclaim is accepted** — fencing not worth it for v1.
7. **Walk-up bound is `monitor.ProjectPath(projectID)`** — stop at the registered project root.

## Open follow-ups (file as separate tasks)

These are NEW tasks to file after plan approval and before/during implementation; none block this slice:

1. **Branch-collision retry strategy** — file with `phase=maybe` (E-1088 has landed). Title: *"Define branch-collision strategy for short-id worktrees if Layer F shortens session-uuid prefixes"*. Layer D doesn't create worktrees so this is purely Layer F's concern; recording it now so it isn't forgotten.
2. **`endless pivot` verb design (Layer F)** — already implied by E-971 as a future slice; ensure a tracking task exists. (Verify before filing — may already exist.)
3. **Update parent E-971.md** — a one-line edit (rename `flavor` → `kind` in the table at line 26 and in the JSON example) lands in this slice's PR; no separate task needed, but call out in the PR body.

## Verification

Manual end-to-end (from a worktree built via `PYTHONPATH=<worktree>/src endless ...` per house rules — never `just install` from a worktree):

1. **Adoption of an unowned worktree.**
   - From main: `git worktree add -b foo .endless/worktrees/foo main` and write a minimal `.endless/worktree.json` (`{"kind":"task","task_id":"E-9999","base_branch":"main","branch":"foo","created_at":"..."}`). No lock file.
   - `cd .endless/worktrees/foo`, start a new Claude session.
   - `cat .endless/worktree.lock` shows the new session's UUID and PID.

2. **Refusal of owned worktree.**
   - Reuse worktree from (1), still owned by session A.
   - In a second pane: `cd` to the same worktree, start session B.
   - SessionStart returns an additionalContext refusal naming session A's PID. `cat .endless/worktree.lock` still shows session A.
   - Attempt a Write in session B → PreToolUse blocks with the lock-owner message.

3. **Stale-lock reclaim.**
   - Reuse worktree from (1). Manually edit `.endless/worktree.lock` to set `pid: 999999` (definitely-dead PID) and a fake session_id.
   - Start a new session, cd to the worktree.
   - SessionStart detects staleness (`syscall.Kill(999999, 0)` returns ESRCH) and overwrites with the new session's lock.

4. **Edit refused in main, with redirection hint when active task has a worktree.**
   - Sub-case (a): in main with no active task → `Write` is blocked with the no-task message.
   - Sub-case (b): in main with active task E-XXX whose worktree exists → `Write` is blocked with the message including the redirect hint pointing at the worktree path and explaining the `cd` + absolute-path pattern.

5. **Edit refused when active task elsewhere.**
   - cd into worktree A (task E-AAA). `endless task start E-BBB` from inside Claude (E-BBB has its own worktree). Attempt a Write to a file in A.
   - Blocked with the active-task-elsewhere message pointing at E-BBB's worktree.

6. **Edit allowed in owned worktree with matching task.**
   - From a session-claimed worktree whose task_id matches the session's active task: Write succeeds (existing tracking_mode rules permitting).

7. **Task-companion mismatch.**
   - Worktree's companion task_id is "E-XXX", but session.ActiveTaskID is YYY. Write attempt → blocked with task-mismatch message.

8. **SessionEnd releases the lock, leaves the worktree.**
   - End a session that holds a lock. `.endless/worktree.lock` is gone; `.endless/worktree.json`, branch, and worktree directory all persist.

9. **Existing behaviors unchanged.**
   - `go test ./internal/monitor/... ./cmd/endless-hook/...` passes (existing companion_test and any hook tests).
   - E-1012 git-commit-on-main block still fires.
   - Drift-detection (E-917) still fires when active.
