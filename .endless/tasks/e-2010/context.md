ED-1547: no id that anything outside its own row refers to is ever reused.
Removal sets `removed = 1` rather than DELETE, and every read path then filters
on it.

## The original evidence (2026-08-20)

E-1929 shipped ED-1547 for TASKS ONLY. Its own description said "entities whose
ids something else refers to — tasks, sessions and decisions — are no longer
deleted", but only `tasks` got the column, the view and the read-path audit.
Measured against the real ledger that day: `removed` present on tasks, absent on
sessions, decisions and projects.

195 sessions and 1,616 activity rows vanished when two junk project rows were
unregistered by hand that day. ED-1547 has since been amended to name projects
explicitly and to cover any table added later that meets the same test.

## Revised 2026-11-07, after this task sat unlanded through ~8,600 commits

The task was written when all four entities were open. Three of those four have
since been answered elsewhere, by other tasks, and the remaining scope is
smaller and sharper than the original title.

**Projects: done, by a different design, and kept that design.** E-2251 gave
projects `live_projects AS SELECT * FROM projects WHERE status NOT IN
('ignored','forgotten')`, and E-2349 gave `project forget` a tombstone — name
and path rewritten to `forgotten-<id>`, identity and slug cleared so the UNIQUE
indexes admit a re-registration, the originals kept as JSON in `forgotten_from`.
`unregister` and `purge` no longer destroy anything.

Measured against the "act like it never existed" test, that design's two statuses
split: `forgotten` IS `removed = 1` and could be replaced by it with nothing
lost, but `ignored` is NOT. An ignored row is a positive assertion — "this is
not a project, do not auto-register it" — and the directory-to-project resolver
has to FIND it, because being found is exactly what stops auto-registration.
Unifying on `removed = 1` would restore the auto-registration E-2251 exists to
prevent. Projects are also machine-specific rather than project-specific and are
headed for their own database, which is the better place to settle it. So
projects are left alone here.

**One project hard delete survives, and it is automatic.** The disk-gone sweep
in `endless.project_sync` still runs `DELETE FROM projects`, while `tasks` and
`activity` still carry ON DELETE CASCADE from `projects` — the exact mechanism
that cost the 195 sessions, now firing without anyone typing a command.
`monitor.ClearIgnoredDirectory` deletes too, guarded only on the project having
no tasks, so a project with sessions and activity but no tasks is deleted and
cascades them. These are the live bugs and they are the reason the task stays
open.

**Decisions: not done, and the semantics are settled.** Three hard deletes
remain, there is no column and no view, and the explicit `MAX(id) + 1`
allocator still re-mints. `removed` is the right mechanism rather than a status
value: `removed = 1` means "act like this decision never existed", while
proposed, accepted, rejected, supersedes and reconsidered are all workflow.
Settled by the user on review, against the open work to evolve decisions into
decide-tasks, which is orthogonal.

The hazard is real but has not fired: there is no command that removes a
decision, the ledger holds 7 `decision.deleted` events and all 7 target the same
id (dev churn during one task), and that id is an interior hole — only the top
of the range is ever re-freed — so `max_id = 1652, n = 245` and no decision id
has ever actually been reused. Which is why this half is cheap rather than
urgent, and why no removal CLI is added with it.

**Sessions: not done, nothing to convert, deliberately deferred.** Nothing
removes a session today, so there is no live bug; the cost is 82 non-test read
sites across 36 files. Filed as its own task rather than folded in here, so the
project data loss above is not held behind the largest audit of the four for the
one entity that cannot yet lose anything.

**The earlier branch is discarded, not resumed.** It was written against
`internal/schema/changes/` (replaced by goose in E-2019), before the projects
design existed, and with a `tests/tasks/e-NNNN-verify.sh` gate (replaced by
`.endless/tasks/<id>/verify.toml`). Eight of the files it edits have moved or
been deleted in main.
