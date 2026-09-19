# E-1917 (reopened) — fix the post-land defects

The feature shipped and works mechanically, but is defeated in its primary
scenario. Three defects are recorded in the analysis; two of them share one root
cause and one fix. See the analysis for evidence.

## Fix 1 — suppress only when the AGENT ITSELF made the change

Covers **Defect 1** (tmux sibling-pane inference) and **Defect 3** (`esu`
exporting `ENDLESS_SESSION_ID`). Same root cause, one fix.

### The rule

Stamp `tasks.changed_by_session` **only when the mutating process is the agent**.
Leave it NULL for every other origin.

Detection is one environment variable: `CLAUDECODE=1`, which Claude Code exports
into the subprocesses it spawns. Verified: an agent's own Bash tool sees
`CLAUDECODE=1` with `ENDLESS_SESSION_ID` unset; a user's shell — including one
where `esu` has exported `ENDLESS_SESSION_ID` — does not, because that variable
comes from the harness, not the shell. It is the same signal layer 2 of
`_current_endless_session_id` already trusts for exactly this question.

Every other origin is a person at a keyboard and must notify every holder,
including the adjacent agent:

- **Layer 1** — `ENDLESS_SESSION_ID`, exported by `esu` / `endless shell-init`.
  This is Defect 3, and it is the worst of them: `endless guide` *instructs* the
  user to run `esu`, so following the documented workflow is what converts a
  user's own edit into a change attributed to the agent holding the task.
- **Layer 3** — a pane match.
- **Layer 4** — sibling-pane inference (E-1294). This is Defect 1.

### Where it goes

`stampTaskActor` in `internal/events/executor.go` reads `CLAUDECODE` from its own
environment and stamps NULL when it is not `1`:

```go
if os.Getenv("CLAUDECODE") != "1" {
    actor = nil   // a human made this change; suppress nobody
}
```

**Not a new field on the event `Actor`.** The alternative — having Python record
"this was resolved in-process" on the event — is more explicit and would leave a
permanent record in the ledger, but it changes the event schema. Old events would
lack the field, which is precisely what the event-upcasting pipeline (E-1671 /
E-1674, both unbuilt) exists to handle. Reading the environment keeps this fix
inside the executor and out of that dependency.

The cost is honest: the ledger will not record *why* a change was or was not
attributed. If that record is wanted later it is a separate change on top of a
working fix.

`evt.Actor.SessionID` keeps its current meaning, and its other consumer
(`upsertSessionTask`, which WANTS the inferred credit) is untouched. That
separation is the point: `ENDLESS_SESSION_ID` answers "which session's worktree
should this command route through", which is a different question from "which
agent made this change". One value was serving both.

### Why this cannot regress

The gate is **subtractive**: it can only turn an attribution into NULL, never
create one. So no change that is currently notified can become suppressed. The
only possible movement is suppressed → notified, which is the direction of the
bug being fixed.

The failure mode also inverts. Today a missed detection SILENCES the session
that needs the notice. After the fix, a missed detection means `CLAUDECODE` is
absent, the actor is NULL, and everyone is notified — a redundant line, not a
silence.

### Rejected alternatives

- **Drop suppression entirely.** Cannot silence anyone, but self-notices are not
  free: a session changes task state several times near the end of a task
  (`unverified`, plan attach, outcome), and each would return as an FYI next
  turn. Kept in reserve as a two-line stop-gap only.
- **TTY detection** (interactive invocation → human → NULL), suggested in the
  Defect 3 analysis. Rejected: `CLAUDECODE` is more direct, is already trusted
  for this exact question, and TTY detection misclassifies scripted and CI
  invocations.

## Fix 2 — stop writing and holding notices for dead sessions

Covers **Defect 2**. 553 of 679 rows (82%) are undelivered, and the largest
holders are `ended` sessions that will never take another turn.

### Write side

Filter the trigger's fan-out on session state. `tasks_notify_sessions` selects
every `session_tasks` row for the task; add a join to `sessions` and exclude
`ended`.

Only `ended` is excluded. `idle` and `needs_input` sessions can still take
another turn, and their notices are legitimately pending — that is the whole
point of one-shot delivery surviving until the session comes back.

### Reap side

A write-time filter is not sufficient: a session can end between write and
delivery, stranding rows. Add `ReapNoticesForEndedSessions`, called
opportunistically from the hook alongside the reapers already there
(`ReapDeadTmuxPanes`, `ReapWorktreesForProject` in
`internal/hookcmd/claude.go`) — same pattern, same call site, cheap when there
is nothing to reap.

Its first run clears the existing backlog, so no change file or one-off backfill
is needed.

### Retention — decided: no age-based expiry

Notices for a merely `idle` session are NOT expired by age. An idle session can
come back and its pending notices are legitimate; the `ended` filter plus the
reaper removes the bulk of the garbage. Revisit only if the table grows again.

## What does not change

- The trigger's change detection, JSON shape, and elision sentinel.
- One-shot delivery and the `notified` flag.
- The active-task line (Arm 2).
- The delivery log (Arm 3).

## Verification

Extend `tests/tasks/e-1917-verify.sh`; the existing 20 checks must keep passing.

New checks:

1. **Defect 1 / 3, the regression itself.** A task edit made with `CLAUDECODE`
   unset stamps a NULL actor and notifies EVERY holder — including the session
   the resolver would have credited. Run it once with `ENDLESS_SESSION_ID` set
   to a holding session (the `esu` pathway, Defect 3) and once relying on
   sibling-pane inference if reachable in the harness (Defect 1).
2. A change made with `CLAUDECODE=1` stamps the actor and suppresses that
   session alone.
3. An ended session receives no new notice rows.
4. The reaper deletes undelivered rows for ended sessions and leaves `idle` /
   `needs_input` rows alone.
5. A session that ends AFTER its notice is written has that row reaped.

Plus the project-wide regression (`just build`, `go vet`, `go test ./...`,
`just test`).

## Note on the verification gap

The shipped suite passed 20 checks and still missed all of this. It seeded
`changed_by_session` directly with SQL, which tested the trigger's logic but
never the question that mattered: *does the value arriving in that column mean
what the design assumes?* Check 1 closes that gap by driving the real resolution
path with a real environment rather than asserting on a hand-planted value.

## Out of scope — the delivery question

The Defect 3 analysis records two notices that were rendered, logged and marked
`notified = 1` while the session appears not to have acted on them, and leaves
open whether injected `additionalContext` is recorded in the transcript at all.
That is not settled here and is not part of this fix. The hook side is confirmed
working (a real UserPromptSubmit payload returns correct top-level
`additionalContext`), so if a delivery gap exists it is above the hook, and
`.endless/logs/session-notices.jsonl` plus a controlled injection test is how to
settle it. File separately if it proves real.
