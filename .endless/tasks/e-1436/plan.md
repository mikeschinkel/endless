# E-1436: Implement `endless project next batch <ops_file>`

## Motivation

The curated next list mutates via granular append-only commands (one event per command). When a session needs to apply many related changes as one logical edit — for example, seeding a fresh list with several lanes and their tasks — running N separate commands produces N events with no shared identity and no atomicity. A reader looking at the event log sees a sequence of unrelated mutations rather than one coordinated edit.

`batch` wraps a multi-command operation in one transaction. Events still emit one-per-command (preserving the event-sourced model), but they share a `batch_id` so the audit trail can identify the logical edit they belong to. Atomicity is via SQLite's `BEGIN IMMEDIATE TRANSACTION`: either all events land or none do.

## Scope

Implement `endless project next batch <ops_file>` as a positional-argument command (no `--file` flag). Behavior:

1. **Read the file line by line.** Skip blank lines. Skip lines whose first non-whitespace character is `#` (comments).
2. **Parse each remaining line** as a subcommand prefixed by `endless project next`. Use shell-style argument splitting (quoting required for args containing spaces or shell metacharacters).
3. **Validate ALL lines parse** before dispatching ANY. Early-fail on malformed lines names the offending line and its parse error.
4. **Resolve session_id once** at the start of the call. The resolver runs as it would for any single command. Accept `--session <id>` on the `batch` command itself as an override.
5. **Generate one `batch_id`** (allocated atomically — typically `SELECT COALESCE(MAX(batch_id), 0) + 1 FROM project_next_events WHERE project_next_id = ?` inside the transaction).
6. **`BEGIN IMMEDIATE TRANSACTION`.**
7. **Dispatch each line** to the normal granular subcommand handler (the handlers shipped by E-1438). Each handler emits its own row in `project_next_events`. Set `batch_id` on every event emitted within this transaction.
8. **`COMMIT`** on success; rollback (handled automatically on Python/Go error exit before COMMIT) on any error from any handler.
9. **Emit a summary** to stdout: line count, event count (= line count typically), and the `batch_id`.

Example `ops_file`:

```
# Seed the curated list for endless
lane add resolver --rationale "Stop silent session-id misattribution" --priority 1
task add --lane resolver "E-1415: DB existence check; loud failure when companion stale"

lane add observability --rationale "Visibility into worktree/session lifecycle" --priority 2
task add --lane observability "E-1391: Worktree lifecycle events + session_worktrees table"
```

On error: report which line number failed, the error from the handler, and that the whole batch rolled back.

## Out of scope

- The granular subcommands themselves (E-1438).
- Generalizing `batch` to other CLI depths (E-1527 will handle that after this lands).

## Design notes

- **`SQLITE_BUSY`** from a concurrent mutation by another session: surface the conflict and exit non-zero. Same handling as the individual granular commands.
- **Per-line parsing** does not invoke a shell — use a shell-style tokenizer (e.g. Python `shlex.split` or Go `mvdan.cc/sh/v3` syntax). Quoting rules match POSIX shell expectations for the human authoring the file.
- **`batch_id` allocation** is an implementation detail. Suggested: a `SELECT COALESCE(MAX(batch_id), 0) + 1 FROM project_next_events WHERE project_next_id = ?` inside the transaction, before dispatching the first line. Alternative: a dedicated `project_next_batches` table if a separate counter is preferred — implementer's call.
- **Handlers must not commit on their own** when called by `batch`. The wrapper holds the transaction. Either pass a "no-commit" flag to the handler, or factor the handlers so the commit is always done by the caller.

## Verification

1. Batch file with 3 valid commands → 3 rows in `project_next_events` with the same non-null `batch_id`; the lanes/tasks/pending tables reflect the changes; commit succeeded.
2. Batch file with 1 invalid command on line 2 of 3 → no rows changed; no events; error message names line 2 and the cause.
3. Empty file → no-op success (no events, no summary).
4. File with only comments and blank lines → no-op success.
5. Quoting: `lane add resolver --rationale "Stop silent session-id misattribution"` in a batch file → `rationale` field contains the full string.
6. Concurrent batch + granular command from another session → one acquires the immediate lock; the other gets `SQLITE_BUSY`. The loser reports the conflict cleanly.
7. Summary line format: e.g. `5 lines processed, 5 events emitted, batch_id=12`.

## Status

Ready to claim after E-1438 lands (granular handlers are the dispatch target).
