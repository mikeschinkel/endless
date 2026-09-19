# E-1197 — Rename .endless/events/ to .endless/db-ledger/ with db-entries-*.jsonl files

## Why this is being filed

On 2026-05-06, during E-1170's land sequence, Claude (this assistant) used `git stash` to clear unrelated state on main so the worktree branch could rebase. The pop kept the stash silently — git's quiet signal that part of the stash couldn't apply — and Claude declared the operation done without diffing the kept stash against the working tree.

Two files silently regressed:
- `.endless/verbs.json`: 8 verb additions (block, detect, port, distill, auto-inject, close, ship, catch) dropped.
- `.endless/events/events-a7f3-000001.jsonl`: 248 events dropped, including the full plan text for E-863, multiple `task.fields_updated` entries, and `task.status_changed` events.

Mike caught the verbs.json regression and pushed back. Forced verification of events.jsonl revealed the same partial-loss pattern — 595 lines in HEAD/working tree vs 843 in the stash. All 248 events were recovered from `stash@{0}` before any permanent loss. Working tree now at 846 lines (843 stash + 3 post-stash events), all valid JSON, monotonic timestamps, no duplicates.

## Why this is a database problem, not a logging problem

Mike clarified after the recovery: `.endless/events/*.jsonl` is the durable write-ahead record for the Endless SQLite database. The DB is rebuilt by replaying these JSONL entries. Lose entries → divergent DB state on next replay. Reorder entries → corrupt rebuild. Drop a file → lose history that no other system records. The "events" naming biases readers (human and LLM) toward "observable telemetry / discardable logs" — exactly the wrong instinct for a file whose loss corrupts the database.

## Naming choice

| Term | Verdict |
|------|---------|
| events, log, event-log, audit log, changelog | All read as observable side-channel telemetry — the existing trap |
| journal | Same trap (cf. systemd `journalctl`) |
| WAL | Strong DB signal but collides with SQLite's own `<db>-wal` files |
| **ledger / entries** | **No log connotation in any major prior art**; canonical accounting pairing; "losing a ledger entry" is catastrophic in every domain that uses the term |

Decision: directory `db-ledger/`, files `db-entries-<id>.jsonl`. The `db-` file prefix keeps individual files self-identifying when they travel out of context (tarballs, backups, bug reports). "Entries" is the canonical pairing for "ledger" so the names reinforce each other instead of repeating a generic prefix.

## Identifier scheme — preserved per prior decision

Mike confirmed there was already a prior decision (not formally recorded; correcting that retroactively here) to use `<host_fp>-<sequence>` for file identifiers rather than full UUIDs. That decision stands. Files become `db-entries-a7f3-000001.jsonl` (current host-fp + 6-digit sequence preserved). Not re-opening UUIDs vs. fingerprint+seq.

## Implementation scope

- Go writer: `cmd/endless-event/...` — change directory and filename pattern.
- Python bridge: `src/endless/event_bridge.py` — match the new path/pattern.
- DB rebuild / replay logic: any code that scans for `events-*.jsonl` needs to look for `db-entries-*.jsonl`.
- Hooks and monitoring scripts that reference the path.
- Tests (pattern matching, fixture paths).
- Docstrings and any internal docs referencing `.endless/events/`.
- One-time migration on existing installs: `mv .endless/events .endless/db-ledger && rename 's/^events-/db-entries-/' .endless/db-ledger/*.jsonl` (or equivalent in code).
- Backward-compat read window: for one release, readers should accept files under both old and new paths so in-flight events emitted before the migration aren't orphaned. Cutover after one release.

## Verification

- Fresh install: writes go to `.endless/db-ledger/db-entries-<host_fp>-000001.jsonl`.
- Migrated install: existing files renamed in place, no event loss, replay produces identical DB state.
- DB rebuild from `.endless/db-ledger/`: matches the pre-rename rebuild byte-for-byte (modulo timestamps).
- Hooks/monitoring still find the file under the new path.
- A subsequent stash dance (or other risky op) on the renamed directory should *no longer* trigger the "this is just logs" misclassification — naming is the load-bearing safeguard.

## Out of scope

- Switching to UUID identifiers (prior decision: keep host-fp + seq).
- Adding a separate manifest file for replay ordering — current filename ordering is sufficient.
- Retroactively renaming git history of `.endless/events/` files. Old commits keep old paths; live tree uses new paths.
