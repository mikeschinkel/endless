# Measured state of `rebuild-db --confirm`, 2026-08-25

Gathered while verifying E-1967 and E-2062. Recorded on this epic so its
children do not re-derive it. Measured against a fresh schema.sql; the abort was
reproduced end to end with the real binary on the modernc driver.

## The command cannot run today, and that is currently load-bearing

`DELETE FROM tasks` tries to null `sessions.task_id`; ED-1560's write-once
trigger aborts that; the abort fails the statement and rolls the whole
transaction back. So the command refuses on any project with a bound session.

That refusal is the only thing preventing the losses below. Full transitive FK
closure of the same DELETE, second hop included:

    task_landings          CASCADE   destroyed, never restored
    session_gates          CASCADE   destroyed, never restored (via epic_id)
      report_judgments     CASCADE   destroyed  (SECOND HOP)
      report_labels        CASCADE   destroyed  (SECOND HOP)
    sessions.task_id       SET NULL  violates ED-1560
    sessions.epic_id       SET NULL
    session_statuses       SET NULL  (dead feature, removed separately)
    decisions.origin_task_id, tasks.parent_id   SET NULL, both restored

Anything that walks only the direct children of `tasks` misses the two
second-hop tables.

## Four gaps, and who owns each

1. The copy-back writes only tasks, decisions and decision_relations, while the
   projector builds task_landings and task_deps as well — and task_landings is
   additionally destroyed by the cascade above. **E-1728.**
2. Mutations outside the task and decision domains never reach the ledger, so
   their tables cannot be rebuilt at all. **E-910.**
3. Replay-side FK and UNIQUE failures, plus committed fixture ledgers reusing
   real task ids 1-150 in roughly 36 of 47 segments, so the INPUT is polluted.
   **E-1041.**
4. The command aborts by accident rather than by intent. **E-2062, landed.**

## Ordering constraint

Removing `sessions.task_id`'s ON DELETE SET NULL is correct on its own terms: it
contradicts ED-1560, and session_tasks already documents the right shape (no FK
at all, so rows outlive their task). But it also removes the tripwire, turning a
loud refusal into silent loss of the four CASCADE tables. It must land AFTER the
rebuild is whole, never before. E-2062 guards the interval, and E-1728 carries
the trap list for the repair itself.



## Refused events are already in the ledger (found by E-2155, 2026-09-17)

A divergence source that exists today, before any upcasting work: **a refused
event is written to the ledger and committed, then refused.**

`event emit` appends the event line and git-commits the ledger segment, and only
then calls the executor, where the guards live (status-transition legality, actor
standing, parent cycles, phase, decision status). A refused change therefore
rolls back in SQLite and stays in the ledger — and the projector's replay of
`task.fields_updated` applies `status` with no transition check at all, so a
rebuild would apply exactly the change the live path refused.

Reproduced in a worktree sandbox: two `task update --status confirmed` calls
against an `unplanned` task were both refused, the task stayed `unplanned`, and
each added a `task.fields_updated` line to the sandbox ledger.

This is the executor/projector parity gap this umbrella names, in its sharpest
form: the guard exists only on the executor side, and the ledger — the source of
truth — records changes that never happened. Every refusal an agent has hit
through those guards is in the main ledger now. Deciding it is part of why a
rebuild cannot be trusted: either the append moves after the executor succeeds
(which changes the events-authoritative ordering deliberately chosen in `emit`),
or replay has to run the same guards the executor does.
