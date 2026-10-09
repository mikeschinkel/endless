# What agents put into task content fields, and a plan-item model

Measured on the main database and its event ledger on 2026-10-09, over the
endless project's live tasks: roughly 590 plans, 1,130 analyses, 930
contexts, 510 notes, 260 reasons and 105 outcomes. Section headings were
tallied and grouped into uses, then a random sample of thirty plans plus every
plan with after-the-fact edits were read. Percentages are shares of plans with
at least one section of that kind. Counts are rough.

## Summary

- `plan` does about a dozen jobs. Only three of them are "the work to do":
  deliverables, approach, and verification. The rest are why-text, decisions
  and rejected alternatives, scope fences, open questions, relations, session
  procedure, and a growing as-built log.
- The plan is rewritten after the work starts on about a third of tasks that
  have a plan, and after the task is finished on about a fifth. Approval
  stores no snapshot, so once work begins nobody can see the plan that was
  approved without replaying the ledger. This, not formatting, is the main
  obstacle to reconciliation.
- Guidance works when it is specific. `## Context` inside plans fell from
  about half of plans in April and May to none from September on, after the
  field table gave context its own slot. The as-built log grew from about 5%
  to about 35% of plans after the discovery rule told agents to record grown
  scope in the plan.
- Agents already itemize. About 65% of plans have a top-level numbered list
  or numbered section headings. Where items were numbered, some todo outcomes
  reconcile against them unprompted ("Shipped all four items: 1. … 4. …",
  E-1246, E-1257). However, the numbered thing is often not the deliverable:
  it can be verification steps, phases, or answers to questions.
- Proposal: plan items as rows in their own table with per-task stable
  numbers (P1, P2, …, numbered like question series), a one-line statement of
  what will be true, an optional verification, and an optional parent. Prose stays in `plan` as the approach. Approval records a
  baseline on each item. Grown scope becomes new items, not prose. A
  reconciliation holds exactly one entry per item.

## 1. What `plan` holds

Ordered by how many plans contain the use. Examples are task IDs.

| Use | Share | Examples | Belongs in |
|---|---|---|---|
| Verification: tests to add, manual smoke steps, a `tests/tasks/e-NNNN-verify.sh` spec | ~72% (verify.sh spec alone ~25%) | E-1126, E-1275, E-1405, E-2222 | plan, attached per item |
| Approach and design: mechanism, schema, CLI surface, the fix | ~70% | E-1114, E-1264, E-2198 | plan prose |
| Work units named by topic ("Deliver — …", "Piece A — …", "4. Tests") | most plans with headings | E-1603, E-1765, E-2259 | **plan items** |
| Why-text: Context, Problem, Symptom, Root cause, Motivation | ~46%, and none since September | E-1011, E-1036, E-1589 | `context` |
| Files and functions touched ("Critical files", "Reused functions") | ~54% in April, ~2% in October | E-1011, E-1291, E-1405 | plan prose |
| Scope fences: out of scope, non-goals, "what this does not do" | ~45% | E-1126, E-1967, E-1994 | **plan items** of kind exclusion |
| Decisions, often credited to Mike ("Decided (Q1 B1, Q2 A+B+C)") | ~36%, ~19% credited to Mike | E-1036, E-1994, E-2222 | `analysis`, or decisions on tasks (E-1868) |
| As-built log: "As built — scope that grew", "[SUPERSEDED … see As built]", "Folded in", "After rebasing onto main", "What changed since the … plan" | ~16% overall, ~35% of plans from August and September | E-1994, E-2175, E-2259, E-1115, E-1967 | reconciliation and items of origin grown |
| Rejected alternatives, "Why not the obvious fixes" | ~20% | E-1589, E-2175 | `analysis` |
| Session procedure: worktree setup, set status, land, "Closing", "When done" | ~16% | E-1114, E-1115, E-1405, E-1456 | handoff template |
| House rules and constraints | ~12% | E-1605, E-1126 ("Discipline") | plan prose, or exclusion items when checkable |
| Relations and coordination prose ("Related", "Sequencing with E-…") | ~8% | E-1036, E-1456 | task relations |
| Open questions | ~9% | E-940, E-2200 | `task_questions` |
| Acceptance criteria as a list | ~3%, all recent | E-1994, E-2193 to E-2199 | **plan items** (each criterion is one) |
| Research request: questions to answer, inputs, deliverable | ~4%, i.e. most research plans | E-2187, E-2257, E-2279 | plan, as the guide prescribes |
| Line-number citations, which the guide forbids | ~32–47% through June, zero in October | E-1765 | nowhere (enforced on write now) |

Shapes worth knowing:

- **Two plan styles coexist.** *Step lists* (E-2222, E-2242, E-2259): short
  preamble, numbered items, a tests list. *Design essays* (E-2198, E-2175):
  sections named "The fix", "Where it goes", "Cost", with the commitments
  scattered through the prose and sometimes collected under "Acceptance". The
  step-list style can be itemized by moving text. The essay style cannot be
  itemized without someone deciding what the commitments are.
- **Numbered is not the same as itemized.** In E-1405 and E-1126 the numbered
  list is the verification steps. In E-1031 it is phases. In E-2269 the
  headings are Q1–Q7. In E-987 it is "resolution actions taken". A check that
  looks for numbering would pass all of these and fail E-2198, whose
  "Acceptance" bullets are the cleanest set of deliverables in the sample.
- **Items nest.** E-2222's item 2 has sub-items A, B and C. E-1765 has
  "Deliverable 1" and "Deliverable 2", each with numbered steps. One level of
  nesting covers every case in the sample.
- **Some plans hold several plans.** E-1115 has two H1 titles, the current
  delivery on top and the "Original … plan (superseded)" below it. E-987
  says "This text replaces the v1 plan body and the addendum."
- **Edits mark items by hand.** E-2259 item 4 is prefixed
  `[SUPERSEDED 2026-10-07 by Mike — see As built]`, with the reversal written
  as a trailing bullet. That is per-item status tracked by hand, which is
  exactly what rows would carry.

Lifecycle, from the ledger:

- About 607 tasks have ever had a plan, written about 1,520 times in total.
  Roughly 230 were written once.
- About 200 tasks had their plan rewritten while `underway`, and about 120
  after reaching a finished status (`confirmed`, `assumed`, `completed`,
  `declined`, `obsolete`). Some of the post-finish writes are bulk cleanups,
  such as stripping line numbers, but E-1994, E-2175 and E-2259 show the
  normal case: the record of what shipped is appended to the plan.
- `task approve` changes only the status. It stores neither a copy nor a hash
  of the plan. The material-edit rule in `update_plan` (E-1993) treats any
  change beyond whitespace as material, and only on `ready` tasks. It
  deliberately stops applying once a task is `underway`, because the
  discovery rule tells sessions to record grown scope there. As a result, the
  approved plan and the as-built plan share one field, and the second
  overwrites the first.

## 2. The other fields

- **`context`** is the healthiest field: short (median a sentence or two),
  single-purpose, almost never headed. It holds today's behaviour, the
  incident and the evidence, as intended (E-2017, E-2123, E-2200). The one
  misuse is a single trailing fragment ("Independently useful for filtering;",
  E-1813), which looks like a split description.
- **`analysis`** holds four things:
  - text moved out of over-long descriptions under `## From the description`,
    on about 200 tasks (E-2168, E-1582), which is migration residue;
  - one-line fix directions ("Fix: drop the trailing HEAD arg…", E-1355,
    E-2053);
  - scope and out-of-scope notes (about 80, E-2214, E-2280);
  - planning input and evidence (E-1736).

  431 tasks have both an analysis and a plan, and scope fences and decisions
  show up in both.
- **`notes`** is mostly not notes:
  - about a third is relation rationale ("Implements E-…", "Split out of
    E-…", "Distinct from E-1306 … coordinate edits", "Blocked by E-2137
    because …", E-1591, E-2138), meaning a reason attached to a link with
    nowhere else to live;
  - 35 are the `--justification` block;
  - about 35 are dated history and progress;
  - the rest is short asides ("Mike pushed back on 'will deprecate'
    framing").
- **`outcome`** is used as designed on research and brainstorm tasks (E-1552,
  E-1715, E-1952), and as a ship report on todo and bugfix tasks (E-1246,
  E-1257, E-1360, E-1714). On a todo, it sometimes holds what should be a
  reopen note ("Reopening: per Mike, tests should attach to this task",
  E-1115).
- **`reason`** is clean: one or two sentences on why a task ended.

Uses that recur across fields, which a content model should give one home:

| Use | Found in |
|---|---|
| Scope fences | plan, analysis |
| Decisions and rejected alternatives | plan, analysis, notes, outcome |
| Relation rationale | notes, plan |
| Dated history and as-built | plan, notes, outcome |
| Verification | plan, outcome ("Gate FIRES. tests/tasks/…", E-1714) |

## 3. Should plan items be rows? Yes.

Text cannot carry identity across edits. A reconciliation must name the item
it answers, and that name has to survive reordering, rewording and insertion.
Rows give that, and the existing `task_questions` series already sets the
precedent for per-task numbering.

Proposed table, `task_plan_items` (projected from the ledger like everything
else):

| Column | Meaning |
|---|---|
| `id` | global key |
| `task_id` | owner |
| `seq` | per-task number, assigned on creation and never reused or renumbered. Rendered `P3`, or `E-2280.P3` from outside the task. |
| `parent_seq` | optional, one level only (E-2222's 2.A). A parent is reconciled through its children. |
| `sort_order` | display order, separate from `seq`, so reordering never renumbers |
| `kind` | `deliverable` (something will be true) or `exclusion` (something will not be touched or done). Exclusions turn the 45% of plans with scope fences into checkable commitments. |
| `statement` | one line, short like a description: what will be true. "`worktree land --check` prints exactly one Landable line and exits 0 on a clean branch." When the plan requires a particular way of doing it, the statement says so ("…compiled in the throwaway rehearsal checkout, never in the live worktree", E-2222). Reconciliation is measured against what the statement says and nothing more: doing something the statement does not specify, in whatever way, is not a deviation. |
| `verify` | optional: how this item is shown to hold, such as a test name, a verify-suite check, or a command. Replaces the separate tests list most plans carry. |
| `detail` | optional short prose: the files or functions it touches. Long design stays in the plan prose. |
| `origin` | `planned` (exists at approval) or `grown` (added after claim) |
| `state` | `active` or `withdrawn` (withdrawn rows stay visible) |
| `approved_statement`, `approved_at` | the baseline, copied on `task approve` (see section 5) |
| `created_by_session`, timestamps | provenance, as on questions |

The `plan` field stays, as the approach: design, rationale for the approach,
files, constraints. `task show --plan` renders the items first, then the prose.
CLI shape, mirroring `question`: `endless plan item add <id> "<statement>"
[--exclusion] [--verify …] [--parent P2]`, plus `edit`, `withdraw`,
`move` and `list`. Also `--items-file` on `task update`, taking one item per
top-level list entry so a session can still write a plan in one go. The
parsing is deterministic because the file format is defined, not guessed.

## 4. Reconciliation against items

Two tables:

- `task_reconciliations`: `id`, `task_id`, `session_id`, `created_at`, and
  `tree` (the branch's tree with `.endless/` excluded, as the brainstorm
  settled).
- `task_reconciliation_entries`: `reconciliation_id`, `item_seq` (never
  null), `verdict` (`as_planned`, `differently`, `not_done`, `blocked`),
  `note`, and `question_id` (nullable, foreign key to `task_questions`).

Extra work is not a free-floating entry. It is an item with `origin = grown`
that the session adds when it does the work, which is what the discovery rule
already asks for, moved from prose to a row. That keeps one rule: **every
entry names an item, and every live item has exactly one entry.** It also
removes the "As built" sections that make up a third of recent plans.

The gate (a single function, called from every hand-over path and from
`task verify`) passes only when all of the following hold:

1. The latest reconciliation exists, and its `tree` equals the branch's
   current tree with `.endless/` excluded.
2. Every `active` item, planned or grown, has exactly one entry. A parent's
   entry is implied by its children's.
3. Every entry whose verdict is not `as_planned` has a `question_id`, and
   that question is answered or closed.
4. Every `grown` item has either its own answered question of kind "more",
   or `approved_statement` set (the user approved it).
5. Every `exclusion` item marked `not_done` (the fence was crossed) has an
   answered question.

`task_questions` gains a `kind` column (`less`, `blocked`, `different`,
`more`) so questions sort by concern in the order the brainstorm set. "No
discrepancies" is recorded affirmatively: it is a reconciliation in which
every entry is `as_planned` and no item is grown and unapproved.

## 5. Items across the lifecycle

- **Before approval** (`unplanned`, `submitted`, `revisit`): items can be
  added, edited and withdrawn freely. No baseline exists.
- **Approval**: `task approve` copies each active item's `statement` into
  `approved_statement` and stamps `approved_at`. Refuse approval of a
  todo, bugfix or docs task with no active deliverable item.
- **Material edit on `ready`**: material means the item set differs from the
  baseline: an item added, withdrawn, or with a changed statement or kind. Edits to plan prose stop being material. That replaces today's
  whitespace comparison with a deterministic test that tracks the
  commitments. The task drops to `submitted`, as now.
- **While `underway`**:
  - a new item is `grown`;
  - an approved item's statement cannot be edited, and a deviation is
    recorded as a `differently` or `not_done` entry, not by rewriting the
    commitment;
  - withdrawing an approved item is refused (it is `not_done` until the user
    agrees);
  - the user can re-approve, which re-baselines everything, including
    grown items.

  Together these rules stop the approved plan from being silently overwritten
  (about 200 tasks today).
- **After shipping** (`unverified` onward): items and entries are frozen. The
  record of what shipped is the reconciliation, plus `outcome` when prose is
  wanted. Plan prose stays editable with `--keep-status` for typo-level
  fixes. A user reopen (`revisit`) unfreezes items.
- **Research and brainstorm**: out of the gate's scope, as settled. The same
  table could later hold "questions to answer" (E-2279's own six are
  numbered), with the outcome answering each one, but nothing here depends on
  that.

## 6. Existing plans

Settled with Mike: **refuse approval and spawn of a todo, bugfix or docs
task with no active deliverable item, with no migration and no grandfathering
at those two points.** Claim is not gated: a session that claims such a task
can write the items itself as part of the work.

- The guide's plan gate already sets the precedent: "There is no
  grandfathering — an older task meets the gate exactly as a new one does."
  The open todo and bugfix tasks that have a plan are about 60 that have not
  started (`submitted`, `ready`, `unplanned`) and about 20 underway or
  unverified.
- Not-yet-started tasks meet the item requirement when approved or spawned.
  `task prime` and the planning session already exist to draft the missing
  piece, and drafting items from a prose plan is that kind of job.
- A task claimed without items still cannot hand over a verify: the
  reconciliation gate also refuses a todo, bugfix or docs task with no active
  deliverable item, so a claiming session that skips writing items is caught
  there.
- Underway and unverified tasks, and finished ones, are left alone. The
  reconciliation gate skips a task that has no items and was claimed before
  the feature shipped, keyed on the claim event's time, so the cutoff is
  data, not judgment.
- No automatic migration of the roughly 590 existing prose plans. Extracting
  items from essay-style plans needs judgment (section 1). A parser would be
  the non-deterministic gate the brainstorm rejected, moved one step earlier.

## Settled with Mike after the findings

1. **No firmness flag.** A deviation is anything that contradicts what an
   item's statement says. If the statement does not say how something is to
   be done, doing it any particular way is not a deviation; if the plan
   needs a particular way, the statement says so. Every `differently` entry
   therefore needs an answered question, as the brainstorm set.
2. **Exclusions are items** (`kind = exclusion`), checked by the
   reconciliation like deliverables.
3. **Each item carries its own optional `verify`**, replacing the separate
   tests list.
4. **Gate points: approval and spawn**, not claim (section 6).
5. **Scope: todo, bugfix and docs.** The docs type is not in `task_types`
   yet, but is about to be added, so the item requirement and the
   reconciliation gate cover it alongside todo and bugfix.

Related clean-ups this study points at (not filed):

- relation rationale in `notes` wants a note on the relation row;
- session procedure in plans wants the handoff template;
- the migrated `## From the description` blocks in `analysis` remain as
  residue.
