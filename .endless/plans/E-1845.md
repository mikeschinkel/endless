# E-1845 — Add `untriaged` status upstream of `unplanned`

**Shipped.** Prerequisite for E-1859 (the description-sufficiency triager),
which unblocks the moment this lands.

`tasks.status` is plain TEXT validated in application code (schema.sql carries
no CHECK constraints and there is no status enum table), so this was a code +
docs change with **no migration and no backfill**. Existing rows keep their
current status; only newly filed tasks start at `untriaged`.

## Decisions settled during implementation

**1. Named `untriaged`, not `unevaluated`.** The docs had provisionally used
`unevaluated`; Mike renamed it at implementation time. "Triage" is the
better-known word for the gate where incoming work gets looked at before it
enters the real queue — a reader knows what `untriaged` means without being
told. The four doc files, this task, and E-1859's description were renamed to
match. The mermaid transition labels read "triage routes (…)", naming the act
rather than a component, so E-1859 is free to name its job whatever it likes.

**2. No shipping deadlock — the manual route is the duplicated slice.**
`untriaged` is in `_SUBMITTABLE_FROM`, so a human can route with
`endless task submit` before E-1859 exists. That overlap is deliberate and
permanent: it is the human override for a triage call you disagree with, not
scaffolding to remove. Section C of the verify script pins it.

**3. Description-edit reset fires from pre-work statuses only, on a material
change.**

| Fires from | Never fires from |
|---|---|
| `untriaged`, `unplanned`, `submitted`, `ready`, `revisit` | `underway`, `unverified`, `confirmed`, `assumed`, `completed`, `declined`, `obsolete` |

Guards mirror E-1762's auto-revisit: identical rewrites no-op, `--keep-status`
suppresses, an explicit `--status` wins. `ready` IS included (approval was
granted against the old description); `underway` is excluded so an edit cannot
yank work out from under a live session.

**Composition, discovered in test:** re-spec + plan-attach in the SAME call
lands `submitted`, not `untriaged` — a task carrying a full plan must not read
as "nobody has looked at this". This is resolved in Python, because the
executor's plan-attach auto-move only fires when the update does not set status
explicitly, and the reset does set it. A `ready` task still loses its approval
this way, landing `submitted` for re-approval — correct.

**4. `task next` OMITS it** (Mike's call). The original plan said "rank after
revisit", which contradicted its own verification check 6. `task next` has been
largely superseded by `session status` and may be deprecated later; omitting is
the simpler, consistent answer. The tree backlog and the web "next actions"
widget omit it for the same reason. An untriaged task still **blocks** its
dependents — unfinished work is unfinished whether or not it was looked at.

**5. Epic derivation gains its own rung** (below `unplanned`). Without it, an
epic whose children are all freshly filed matches no bucket and is left
unchanged — the common case now that `untriaged` is the default, not an edge
one.

**6. `schema.sql`'s column DEFAULT stays `'unplanned'`** (Mike's call). Changing
a SQLite column default requires a table rebuild, which the no-migration
constraint rules out. Every insert path supplies status explicitly, so the
default is dead code. Align it whenever `tasks` is next rebuilt.

## Sites changed

### Python
- `cli.py` — `TASK_STATUSES` (feeds five `click.Choice`/`MultiChoice` option
  types for free); `--status` help strings.
- `task_cmd.py` — `add_item` default; `_SUBMITTABLE_FROM`; `update_plan`'s
  `valid` tuple; `next_tasks` WHERE exclusion; tree glyph `◌`; tier-1
  auto-advance; `_CHILDREN_STATE_ORDER`; the new `_DESCRIPTION_RESET_FROM` +
  reset logic beside E-1762's auto-revisit.
- `session_status_cmd.py` — `_VALID_STATUSES`.

### Go — beyond the original plan's site list
Three sites the plan did not name would have broken:
- `events/executor.go` — the plan-attach auto-move was gated on `unplanned`, so
  `task add --text plan.md` would have filed a fully planned task as untriaged
  and stranded it. Extracted `isPreJudgmentStatus` for both call sites.
- `events/epic_derivation.go` — the new rung (decision 5).
- `monitor/session.go` — claim promotes `untriaged` → `underway`, else a
  claimed task reads untouched while someone is actively on it.

Also: `events/session_status.go` (Pending disposition), `monitor/task.go`
(`GetActiveTasks`), `web/handlers.go`, `web/queries.go`, both templ pages.

### Go — `sessionstatuscmd`
New `actTriage` **appended** to the enum (enum order is legend order and
`sortRows`' rank, so appending leaves every existing rank untouched), glyph `◌`
(single-width, asserted), label `triage`. `classify()` gains an explicit
`case "untriaged"` — load-bearing, since falling through to `actUnknown` would
paint `⁇` (the should-never-happen marker) on the most common row in the ledger.
It is deliberately NOT `actPlan`: "needs a plan" is a judgment nobody has made
yet, and that distinction is the entire reason the status exists.

### Docs
`docs/status-lifecycle.mmd` is canonical; the three embedded copies were
re-synced byte-identically. The prose status table in `docs/guide/index.md`
gained an `untriaged` row, and both it and `CLAUDE.md` now document the
description-edit reset and `--keep-status`.

## Adjacent pre-existing bugs found (filed, not fixed inline)
- **E-1888** — `submitted` missing from `_CHILDREN_STATE_ORDER`, so a submitted
  child is counted in the total but never rendered as a bucket.
- **E-1891** — `task update --status submitted` is rejected as invalid; the
  `valid` tuple duplicates `cli.TASK_STATUSES` and has drifted from it.
- **E-1894** — `tests/tasks/*-verify.sh` rot silently. `e-1648-verify.sh` was
  dead (called `endless register`, renamed to `project register`, so it exited 2
  at setup) and carried an assertion E-1765 superseded. Both fixed here because
  this task's rename touched the file.

## Verification
`tests/tasks/e-1845-verify.sh` — 63 checks, fail-fast (section A runs the Go +
Python unit tests first and aborts the run if they fail). Sections B–J cover the
add default, the no-deadlock guarantee, the description-edit reset and all three
guards, the `◌ triage` render on a non-focal row, `task next` omission with
blocking retained, epic derivation, doc sync + prose, cross-surface
registration, and a project-wide regression.
