# E-1202 — Block direct Write/Edit of plan files via a preToolUse gate

## Context

Two standing lessons — "no plan files direct to main" and "plan files via
`task update`" — tell an agent never to hand-write `.endless/plans/E-NNN.md`.
`tasks.text` is the source of truth; the `.md` file is a *mirror* that endless
writes and commits during worktree-land so humans can read plans on GitHub. A
direct Write/Edit of that mirror leaves the DB stale, and if the worktree is later
dropped the edit is silently lost. This task converts those two soft lessons into a
deterministic preToolUse gate so the mistake is structurally impossible, then prunes
the now-redundant memory files.

The gate must fire for the canonical mirror path in **any** tree — main's working
tree *and* a worktree's materialized mirror — because neither should be hand-edited.
It blocks only Claude's Write/Edit/NotebookEdit tool calls; the CLI's own
materialization (`_materialize_plan_file` in `src/endless/worktree_cmd.py`) is a
Python subprocess the hook never sees, so the land-produces-plan-markdown flow is
untouched.

## Mechanism

A new gate in `internal/hookcmd/claude.go`, modeled exactly on the existing
`blockSqliteAgainstEndlessIfApplicable` / `blockCommitOnMainIfApplicable` gates:
a package-var regex + a small `blockPlanFileWriteIfApplicable(payload)` helper that
pulls the target path via the existing `extractFilePath` and, on match, calls
`blockToolUse(msg)` (stderr + exit 2 — the always-works form all sibling gates use).

## Changes

### `internal/hookcmd/claude.go`

1. **Add package var** near the other gate regexes (`gitCommitRe`, `sqliteEndlessRe`):
   ```go
   // planFileRe matches a task plan-file mirror path .endless/plans/E-NNN.md,
   // absolute or repo-relative. Anchored $ + the E-<digits>.md immediately after
   // plans/ excludes any subdir (e.g. a future plans/snapshots/…) and any non-plan
   // file in the dir. Plan files are the DB-owned mirror endless writes on land;
   // hand-editing them via Write/Edit desyncs tasks.text (E-1202).
   var planFileRe = regexp.MustCompile(`(^|/)\.endless/plans/E-\d+\.md$`)
   ```
   Confirmed on disk: mirror files are uppercase `E-NNN.md`; the Python
   materializer writes `E-{task_id}.md`.

2. **Add helper** next to `blockCommitOnMainIfApplicable`:
   ```go
   func blockPlanFileWriteIfApplicable(payload claudePayload) {
       path := extractFilePath(payload.ToolName, payload.ToolInput)
       if path == "" || !planFileRe.MatchString(path) {
           return
       }
       blockToolUse(<message below>)
   }
   ```
   Reuses existing `extractFilePath` (handles Write/Edit `file_path` +
   NotebookEdit `notebook_path`).

3. **Wire into `handlePreToolUse`** — insert AFTER the
   `if !writeTools[payload.ToolName] { return nil }` early-return and BEFORE the
   `enforceWorktreeGate(projectID, payload)` call. Rationale: ordering before the
   worktree gate means a plan-file write in main gets the plan-specific message
   rather than the generic "edits in main" refusal. Scoped to write tools only
   (it's after the writeTools guard); independent of tracking_mode, like the
   worktree gate.

4. **Block message** (no specific internal task IDs — the pattern placeholder
   `E-NNN` is fine per the user-facing-string rule):
   ```
   BLOCKED: refusing a direct Write/Edit of a task plan file
   (.endless/plans/E-NNN.md). Plan content lives in tasks.text (the source of
   truth); the file is a mirror endless writes and commits for you so humans can
   see plans when reviewing the repo on GitHub or other Git hosts. Editing it
   directly leaves the DB stale, and if the worktree is later dropped the edit is
   silently lost.

   Author the plan at a temp path, then run:
     endless task update <id> --text-file <path>

   (--text-file loads the file's content; --text would store the path
   string itself. Use --text only for inline content.)

   Never hand-edit or git-commit the plan file yourself.
   ```
   Note: the message names `--text-file` (loads file content), NOT `--text`
   (which stores the argument verbatim — using `--text <path>` writes the path
   string into tasks.text, a real corruption footgun this gate must not
   perpetuate).

## Tests

### `internal/hookcmd/plan_file_test.go` (new — primary, mirrors `sqlite_endless_test.go`)

Table-driven `TestPlanFileRe` over `planFileRe.MatchString`:
- **Match**: `.endless/plans/E-1.md`, `./.endless/plans/E-1202.md`,
  `/Users/x/proj/.endless/plans/E-999.md`, `/abs/wt/e-1202/.endless/plans/E-1202.md`.
- **No match**: `.endless/plans/snapshots/E-1.md` (subdir excluded),
  `.endless/plans/notes.md`, `.endless/plans/E-1.txt`, `.endless/plans/E-.md`
  (no digits), `src/foo.md`, `my.endless/plans/E-1.md` (not a path component).

### `tests/tasks/e-1202-verify.sh` (new — self-contained, shape per `tests/tasks/e-1577-verify.sh`)

The single handoff command is `esu && ./tests/tasks/e-1202-verify.sh`, so the
script does EVERYTHING — no separate build/test steps for the user to run. It:

1. **Builds** `bin/endless-go` (`just build`) up front so the gate is never
   tested against a stale binary (per the rebuild-reopened-worktree lesson).
2. **Runs the Go unit test** as one section:
   `go test ./internal/hookcmd/ -run TestPlanFileRe` → pass/fail reported.
3. **Drives the worktree hook binary** (`./bin/endless-go hook claude`) with
   synthetic `PreToolUse` JSON payloads and asserts exit code + message. The gate
   is pure path-matching (no DB):
   - Write to `<abs>/.endless/plans/E-999.md` → REFUSED (exit 2; output names
     `task update <id> --text`).
   - Write to `<abs>/.endless/plans/snapshots/E-999.md` → ALLOWED (subdir excluded).
   - Write to `<abs>/.endless/plans/notes.md` and a normal source file → ALLOWED.
   - Edit + NotebookEdit variants of the plan-file path → REFUSED.
4. **Sanity**: `endless task update <id> --text <file>` (via `--db sandbox`) still
   materializes/updates the mirror (CLI path unaffected by the gate).

Uses the pass/fail/summary helper harness from `e-1577-verify.sh`; exit 0 on
all-passed, 1 on any failure.

## Cleanup (on completion, per task description)

Prune the two now-redundant memory files and their MEMORY.md index lines:
- `memory/feedback_no_plan_files_in_main.md`
- `memory/feedback_plan_files_via_task_update.md`

(These live in `~/.claude/projects/-Users-mikeschinkel-Projects-endless/memory/` —
outside the worktree, so a memory edit, not a repo change. Do at completion, not in
the worktree diff.)

## Build & verify

One self-contained command (the script builds, unit-tests, and end-to-end tests):

```sh
esu && ./tests/tasks/e-1202-verify.sh
```

Then: `endless task update E-1202 --status unverified --db main`.
