# Plan — rename tasks.text to tasks.plan, forward path complete

The field has been called "the plan" in every conversation and every guide for
as long as it has existed; only the column, the flag and the event key still say
`text`. This closes that gap. It is sequenced BEFORE E-1531 so the migration
into `task_content` carries one name rather than migrating `text` rows and
renaming them afterwards, and before E-2136 so Agent Folio part names — which
are field names — are never minted as `text`.

Design settled on E-999 (closed `assumed`), which handed this half here.

## Three answers that shape the work

1. **`--text` is removed as a working flag but retained as a RECOGNISED one**,
   erroring with a pointer to `--plan`. Not a hidden alias — both names live at
   once is the thing that lets an obsolete name survive for years — and not a
   bare removal, because Click's "no such option" tells a session nothing and
   agent muscle memory will certainly reach for `--text`.
2. **`decisions.text` is NOT renamed.** A second column of the same name exists
   on `decisions`. E-1868 rewrites decision storage wholesale, so renaming it
   here is work that gets thrown away. Recorded rather than silently skipped.
3. **Legacy ledger replay is handled HERE, not deferred.** An earlier draft
   passed it to E-1728. Reversed: E-1728 belongs in `later`, so deferring there
   would leave the ledger and the schema disagreeing for as long as that sits —
   and the fix is two map entries. The cost of carrying it is lower than the
   cost of the window.

## The forward path — all of it, in this task

**Schema.** Rename the column on `tasks` (line ~287 of schema.sql; the second
`text TEXT` at ~652 is `decisions` and stays). SQLite supports
`ALTER TABLE ... RENAME COLUMN`. Goose migration plus the hand-maintained
schema.sql, which is not yet generated from migrations (E-2021).

**Event payload key.** New events must emit `plan`, not `text`. Without this the
ledger keeps accumulating `text` keys forever and the deferred mapping below
never stops being needed. The key lives in field maps in
`internal/events/projector.go` and `internal/events/executor.go`, both spelled
`"title": "title", "description": "description", "text": "text"`.

**The plan-attach promotion.** `executor.go` keys the promotion that moves a
pre-judgment task to `submitted` off `p.Fields["text"]`, and it must read
`plan`. Scope is narrower than it first looks — the branch only fires when the
same update does not set status explicitly (`if !hasNewStatus`), so a caller
passing `--status` alongside the plan is unaffected. What breaks without it is
plan attachment that relies on auto-promotion, silently, so it is worth a
regression test rather than alarm.

**Go reads.** `monitor.TaskText` → `TaskPlan`. The `t.text` column in the
`session_status.go` query set (roughly ten SELECTs plus the `has_text`
derivation), and `COALESCE(t.text, '')` in `monitor/triage_reads.go`.

**JSON field names.** `has_text` in `internal/sessionstatuscmd/json.go` and
`internal/monitor/triage_reads.go` become `has_plan`. These are output field
names, not a stored contract, and leaving them saying `text` would reintroduce
the two-vocabulary problem in the one place agents read.

**Python CLI.** `--text` / `--text-file` on `task add`, `task update` and the
decision surface at the three option sites; the `--text` display flag on
`task show`; the `--text` search flag; and the `--clear` choice value.

**Agent- and human-facing strings.** `text_chars` → `plan_chars` in the `--llm`
view, the `## Text` heading in `task_cmd.py`, and the `Text:` placeholder label.
The `## Text` heading in `decision_cmd.py` stays, per answer 2.

**Plan mirror files.** `.endless/plans/E-NNN.md` is already named for the plan;
no path changes.

## Legacy ledger replay — included

The db-ledger holds **1,250 events carrying a `"text"` key**. They are immutable
by design. A projector that does not map them writes tasks with empty plans on
replay, silently, since an absent field is indistinguishable from an empty one.

The field maps in `internal/events/projector.go` and
`internal/events/executor.go` therefore carry BOTH keys after this task:

    "plan": "plan"     — events emitted from now on
    "text": "plan"     — the 1,250 historical events

Two entries, one per file. That is the whole of it, which is why deferring it
was the wrong call: the deferral saved almost nothing and left the ledger and
the schema disagreeing about a field name until an unrelated task in `later`
got picked up.

Note this is NOT the same as the plan-attach branch above. That branch reads
`p.Fields["text"]` to decide a promotion and must switch to `plan`; these map
entries decide which COLUMN a payload key projects into and must accept both.

## Verification

- `task add --plan-file` then `task show` renders the plan section.
- `task update --text` exits non-zero naming `--plan`; `--text-file` likewise.
- The plan-attach promotion still fires: attach a plan to an `unplanned` task
  with no explicit status and assert it becomes `submitted`. This is the
  regression the executor branch above would cause.
- `session status` still marks plan-bearing rows; `has_plan` appears in `--json`.
- `just test` and `just test-go`.
- Projector mapping: unit-test that a payload carrying the legacy `text` key and
  one carrying `plan` both land in the renamed column.
- NOT verified here: end-to-end rebuild. It is broken independently of this
  change (E-1728), so the mapping is proven at the projector rather than by
  replaying the ledger.
