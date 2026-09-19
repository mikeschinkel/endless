Folded in E-2065 (2026-08-25), which was filed for the copy-back end of this
same gap before E-1935 and this task were found. Measured evidence from that
work follows.

## Correction to this task's original description

It previously read "rebuild-db never rebuilds task_landings (no data loss)".
The first half is true, the parenthetical is not. `DELETE FROM tasks` CASCADES
to task_landings and the copy-back never puts them back, so a completed rebuild
loses landing history outright. Nobody has hit it only because the command
currently aborts before reaching that point.

## Why the abort exists, and why it must not simply be removed

`DELETE FROM tasks` also tries to null `sessions.task_id`; ED-1560's write-once
trigger aborts that; the abort fails the statement and rolls the transaction
back, so nothing is deleted. That is the only thing standing between the command
and the losses below. E-2062 has since replaced the accidental fuse with a
deliberate refusal.

Full transitive FK closure of that DELETE, second hop included:

    task_landings          CASCADE   destroyed, never restored
    session_gates          CASCADE   destroyed, never restored (via epic_id)
      report_judgments     CASCADE   destroyed  (SECOND HOP)
      report_labels        CASCADE   destroyed  (SECOND HOP)
    sessions.task_id       SET NULL  violates ED-1560
    sessions.epic_id       SET NULL
    session_statuses       SET NULL  (dead feature, removed separately)
    decisions.origin_task_id, tasks.parent_id   SET NULL, both restored

task_deps is a quieter variant: the projector builds it, the copy-back skips it,
and no FK reaches it — so a rebuild silently leaves every blocking relation at
whatever the live database happened to hold.

## Traps for the fix

- `tasks_notify_sessions` is AFTER UPDATE ON tasks. Today's delete-then-insert
  never fires it; switching the copy-back to an upsert WOULD, notifying every
  live session about every task whose projected value differs. Suppress the
  notice triggers around any copy-back; e-1969's change file shows the
  drop-then-recreate shape inside a transaction.
- `task_landings_notify_sessions` is AFTER INSERT, so copying landings back
  announces every historical landing to every live session. The E-2005 schema
  comment calls that replay "harmless" ON THE GROUNDS that rebuild copies back
  only three tables — true today, false the moment this lands. Correct it in the
  same commit.
- A projected task_landings.session_id can name a session this machine never
  had: the ledger is shareable, sessions are machine-local. NULL it on copy
  rather than failing the FK.
- `PRAGMA foreign_keys=OFF` is not the escape. SQLite ignores it inside a
  transaction, so it would fence the whole replacement and leave dangling rows
  on a genuinely-lost task id. MaxOpenConns is 1, so the usual pooled-connection
  objection does not apply; the reason is correctness.

## Sequencing

This must land before `sessions.task_id` loses its ON DELETE SET NULL, and
E-2062's guard should be deleted only once this is done. Both constraints are
spelled out in E-2062's plan. Cause (1) of the build side — the projector
skipping session events — is shared with E-1041, which absorbed E-1035 for the
same reason; settle who owns `ensureSession` before building.



