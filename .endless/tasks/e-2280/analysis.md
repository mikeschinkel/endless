## Settled with Mike in the brainstorm

- **When.** The agent runs the work to completion and does not stop
  mid-implementation for a deviation. While usage is subscription-based,
  Mike is answering other tasks in parallel anyway. Revisit this if Endless
  moves to API-key usage.
- **Reconciliation.** After implementing and before handing over a verify,
  the agent reconciles its work against the plan item by item: done as
  planned, done differently, not done, or blocked. Work outside the plan is
  listed as extra. "No discrepancies" is recorded affirmatively, never
  inferred from silence.
- **Kinds, ordered most to least concerning:** doing less than the plan;
  something it could not do (including being blocked by a hook); a different
  route to the same goal; doing more than the plan. All four are raised.
- **Carrying them.** The reconciliation is the record. Each discrepancy in
  it becomes an open question tagged with its kind, so questions sort in that
  order and plug into the existing open-question machinery.
- **Gate.** One shared check, called by the agent's hand-over (setting
  `unverified`, the verify request from the verify/land rework, and any future
  auto-verify trigger) and by the verify command itself as a backstop. It
  refuses unless a reconciliation exists, every discrepancy question is
  answered or closed, and nothing outside `.endless/` has changed on the
  task branch since the commit the reconciliation recorded. Endless's own
  ledger commits therefore do not make it stale, while any code change after
  a fix forces a fresh reconciliation.
- **Scope.** The todo, bugfix and docs types. Research and brainstorm deliver
  an outcome, not code checked against a plan.
- **Plans must be itemizable.** Plan items become structured rows with IDs,
  so the gate can deterministically check that every item has a
  reconciliation entry. A text check for "numbered items" was rejected: it
  tests formatting, not itemization, and gives false positives and false
  negatives. Mike does not want gates that are not fully deterministic.
- **Sequencing.** This task is blocked on the content-field research and
  builds on the plan-item model it proposes. Mike chose this over shipping a
  text-based reconciliation first, to avoid building something that would
  soon be replaced.
- **One task.** Plan shape, guidance and prompting, handoff wording, and the
  record and gate share one cause and ship together (per the decision that
  agents close more than they file).

## Expected plan items, to be confirmed against the research

1. Plan items stored as rows, as the research specifies.
2. Guide: the `plan` field's definition requires discrete, independently
   checkable items, with rationale moved to `analysis`.
3. Plan-time prompting: `/needs-plan`, `task prime` and the handoff's planning
   instructions ask for itemized plans. Folding `/needs-plan` into Endless is
   a later step, not this task.
4. Handoff and guide wording: replace the post-hoc "say so in your reply"
   reporting, including the discovery bullet's "do it", with "it goes into the
   reconciliation".
5. The reconciliation record and the command that writes it, with each
   discrepancy filed as a kind-tagged question.
6. The shared gate in the hand-over paths and in verify, including the check
   against the recorded commit.
7. After implementation: draft the decision recording the rule. Mike asked
   that it be drafted last, not up front.

## Content model from the research (settled with Mike)

The research's outcome (E-2279) holds the evidence and the full model. In
brief, with Mike's answers folded in:

- **Plan items are rows** in their own table, `task_plan_items`, projected
  from the ledger. Each row has:
  - a per-task `seq`, never reused or renumbered, rendered `P3` or
    `E-NNNN.P3` (numbered like a question series);
  - an optional `parent_seq`, one level deep;
  - a `sort_order`, separate from `seq`;
  - a `kind`: `deliverable` or `exclusion`, where an exclusion is a scope
    fence that the reconciliation also checks;
  - a one-line `statement` of what will be true;
  - an optional `verify`, which replaces the separate tests list;
  - an optional short `detail`;
  - an `origin`: `planned` or `grown`;
  - a `state`: `active` or `withdrawn`;
  - `approved_statement` and `approved_at`, the baseline;
  - provenance.
- **No firmness flag.** A deviation is anything that contradicts what an
  item's statement says. If the statement does not say how something is
  done, the way it is done is not a deviation. If the plan needs a
  particular way, the statement says so.
- **The `plan` field stays** as the approach prose. `task show --plan`
  renders the items first, then the prose. The CLI mirrors `question`: add,
  edit, withdraw, move and list, plus an `--items-file` with a defined
  format.
- **Grown scope becomes `grown` items**, not "As built" prose. Every
  reconciliation entry names an item, and there is no free-floating "extra"
  entry.
- **Reconciliation:**
  - `task_reconciliations` holds `tree`, the branch tree with `.endless/`
    excluded;
  - `task_reconciliation_entries` holds the item, the verdict, a note, and a
    `question_id`;
  - `task_questions` gains `kind`, one of `less`, `blocked`, `different` or
    `more`.
- **The gate refuses unless:**
  - the tree is current;
  - every active item has exactly one entry;
  - every entry that is not as planned has an answered or closed question;
  - every grown item has an answered question or the user's approval;
  - every crossed exclusion has an answered question.

  It also refuses a todo, bugfix or docs task with no active deliverable
  item, unless the task was claimed before this ships.
- **Lifecycle:**
  - Approval copies the baseline onto each item.
  - On a `ready` task, a material edit means the item set differs from the
    baseline. Edits to plan prose are no longer material.
  - While `underway`, approved statements are immutable, withdrawing an
    approved item is refused, and the user can re-approve.
  - From `unverified` on, items and entries are frozen. `revisit` unfreezes
    them.
- **Where it is enforced:** approval and spawn refuse a todo, bugfix or docs
  task with no active deliverable item. Claim does not, because a claiming
  session can write the items itself. There is no automatic migration of
  existing prose plans.
- **Docs type:** not in `task_types` yet but about to be added. The gate
  covers it alongside todo and bugfix.

Mike chose to keep this as one task rather than an epic, accepting its size.
