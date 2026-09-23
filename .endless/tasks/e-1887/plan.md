# Plan — E-1887: record a fault when a Claude hook's database write fails

## The gap, stated once

`monitor.TouchSession` writes two rows on every hook event
(`internal/hookcmd/claude.go`, in `runClaude`):

1. a `processes` row identifying the pane — `(kind=tmux, server_uuid, address)`
2. the `sessions` row, setting `process_id` to that row's id

`sessions.process_id` is the only link the tmux status line resolves a pane
through (`queryActiveTaskForPanes`, `internal/monitor/tmux_lookup.go`). If the
write fails, the link is absent, the bar's query matches nothing, and the bar
renders the same "no Endless session" hint a genuinely unclaimed pane gets.

`hook.Run` handles the error by `log.Printf` + `os.Exit(hookExitCode(err))`.
For a non-blocking failure that is exit 0, and Claude discards hook stderr. So
the failure is invisible by construction — not by oversight. The hook must not
block on a database failure; that contract stays. What changes is that it stops
being silent.

## 1. A fault code

Add one to `internal/faults/codes.go`, modelled on `ErrCodeStatusLineUnavailable`:

- `ID`: the next unused `ERR-NNNN`. 0002/0003/0007/0008/0010/0011/0014 are
  spent; a spent number is never reused, so take the next free one at
  implementation time rather than pinning it here.
- `Slug`: `hook-write-failed`
- `Severity`: `SeverityError` — session identity is wrong until it is fixed,
  and every downstream read silently returns the wrong answer.
- `Title`: "A Claude hook could not write to the database"
- `Remedy`: read the detail for the underlying error; a "no such column" or
  enum-integrity failure means the binary and the database disagree — most
  often a worktree binary older than a schema change (see E-2166), repaired by
  bringing that binary up to main's.

## 2. Where to record it

**One site: the error sink in `hook.Run`** (`internal/hookcmd/hook.go`),
beside the existing `log.Printf`, covering `prompt`, `claude` and `codex`
alike.

Not at the `TouchSession` call site. The sink is the one place every error that
ends a hook invocation already passes through, so a future handler is covered
without remembering to. The narrower placement would record exactly the failure
we happened to hit and nothing else — which is how this class of bug survives a
fix.

Errors that are logged and *continued past* (`WakeSession`, notice reaping, the
report gate) are deliberately out of scope: each already has a stated
non-fatal rationale at its call site, and pulling them in would change this from
"the hook failed" to "something in the hook complained", which is a different
signal with a different noise profile. Revisit per-site if one of them turns out
to matter.

## 3. Fingerprint and dedupe

Fingerprint on the **error text**, not the session or pane — the same choice
`runStatusLine` made, for the same reason. One stale binary fails identically on
every event in every pane; keyed by session it would raise one incident per
session and bury the shared cause. Keyed by error text, the four-week outage is
one incident with a rising count.

`Fields` carries the structured context that must not be in the fingerprint:
`session_id`, `hook_event`, `pane`, and the executing binary's path — the last
because "which endless-go wrote this" is the first question a reader asks, and
our incident was answered entirely by that one fact.

`Summary` is short and lands in the fault row session status already renders.
`Detail` carries the full error.

## 4. What must not change

- The hook's exit code, for every path. `faults.Record` is additive; it never
  turns a non-blocking failure into a blocking one. A test should pin this.
- The `log.Printf` line stays. It is what a developer tailing the hook log
  reads, and the fault is for the user who is not tailing anything.
- `faults.Record` must not itself be able to fail the hook. It is already
  documented as never failing and never returning an error; confirm that holds
  when the database is the very thing that is broken, since that is precisely
  when this fires. If recording needs the database and the database is
  unreachable, this fault cannot be recorded at all — say so plainly in the code
  rather than pretending coverage. See the open question below.

## 5. Verification

`.endless/tasks/e-1887/verify.sh`, following the house shape (isolated repo +
XDG dirs, pass/fail per check, exit 0/1/2):

1. A hook event whose `TouchSession` fails records exactly one fault, with the
   expected code and a `Summary` naming the failure.
2. The hook still exits 0 — the non-blocking contract is intact.
3. Twenty further failing events across three pane ids raise **one** incident
   with a rising count, not twenty.
4. A successful hook event records no fault.
5. `session status` renders the fault row for it.
6. Regression: `just test`, `just test-go`.

Reproducing a failing `TouchSession` without a stale binary is the fiddly part;
the cheapest honest lever is a database whose `sessions` table is missing a
column the current binary writes, built in the isolated fixture.

## 6. The fallback sink, and a notice that survives an unreadable database

This section answers what was an open question: a fault raised *because* the
database failed cannot be recorded through the database. Decided — add the
fallback rather than accept the limit.

### What exists, and why it does not already cover this

`internal/faults/detaillog.go` is a real facility, not ad-hoc: `errors.jsonl`,
append-only, one line per fault OCCURRENCE, ConfigDir-routed so a sandbox keeps
its own, every write best-effort. The `errors` table is the bounded index; the
file carries what the table has no room for. It is modelled on
`internal/monitor/usermachinelog.go` (`user-machine.jsonl`).

It covers nothing here, because `faults.Record` reaches `appendDetail` ONLY
after a successful `upsertIncident`. `database()` failing and `upsertIncident`
failing both `goto end` first. Database unreachable means nothing is written
anywhere at all.

### 6a. Write the file line even when the database write fails

Move `appendDetail` off the success path so a fault always lands on disk. The
complication is shape, not plumbing: a detail line currently carries the
incident `id` and `occurrence` number, and both are assigned BY the database
write that just failed. An unindexed line needs its own discriminator — a null
id plus an explicit marker — so a reader can tell "occurrence 4 of incident 12"
from "this was never indexed". Do not invent a synthetic id; a fake id that
collides with a real one later is worse than an honest absence.

### 6b. A notice the fault row can render without reading the database

`session status` renders its fault row via `faultrow.Render`, which reads the
database — the exact thing that may be down. So the notice cannot be a row from
the table; it has to be derivable from the filesystem alone:

- unindexed entries exist in `errors.jsonl` newer than the clear watermark, OR
- the database could not be read at all

Both collapse to one line in the fault row: something was recorded that this
view cannot show you, and where to look. Keep it to one line — the fault row is
beside live work, and a broken database must not take the pane over.

### 6c. Where "cleared" lives when the database is unreadable

`endless errors clear` marks rows in the table today. That state is unreachable
in exactly the case this section exists for, so the file side needs its own
watermark — a small marker file holding the offset or timestamp cleared up to.
`errors clear` must move BOTH, or a cleared database and an uncleared file will
disagree and the notice will never go away.

### 6d. `errors` command surface

- `errors list` and `errors show` read unindexed entries from the file when the
  database is unavailable, and mark them plainly as unindexed.
- `errors clear` moves the file watermark as well as marking rows.
- Worth considering: a reconcile that indexes orphaned file entries into the
  table once the database is healthy again. Cheap to describe, easy to get
  wrong (double-indexing on repeat runs), so it is called out here rather than
  specified — decide during implementation whether it earns its keep.

### 6e. Verification for this section

7. With the database made unreadable, a failing hook event still appends to
   `errors.jsonl`, marked unindexed.
8. `session status` renders the notice in that state, without erroring.
9. `errors clear` silences the notice, and it stays silenced across runs.
10. With the database healthy, behavior is byte-identical to today — no notice,
    no extra file line beyond the existing indexed one.
