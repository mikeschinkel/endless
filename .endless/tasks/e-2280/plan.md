## Terms

These are defined here. None of them is a glossary term or an accepted
decision yet. The decision drafted in the last item records them.

- **Plan item**: one row in `task_plan_items`. It is a one-line statement of
  something that will be true when the task is done, with a per-task number
  rendered `P3` (`E-2280.P3` from outside the task).
- **Deliverable item**: a plan item whose `kind` is `deliverable`. It states
  something the work will make true.
- **Exclusion item**: a plan item whose `kind` is `exclusion`. It states
  something the task will not do or change, for example "does not change the
  ledger event format". These are the "out of scope" lists, made checkable.
- **Grown item**: a plan item added after the task was claimed
  (`origin = grown`). It is work done beyond the approved plan. An item
  added to a task that has never had any items is `planned`, not grown.
- **Reconciliation**: the agent's record, made after implementing and before
  setting `unverified`, of how the work compares with every active plan
  item. It has one **entry** per item, holding a **verdict**:
  - for a deliverable item: `as_planned`, `differently`, `not_done` or
    `blocked`;
  - for an exclusion item: `kept`, or `not_kept` when the work did the thing
    the item rules out.
- **Discrepancy**: any entry other than `as_planned` or `kept`, and any grown
  item the user has not approved. Each one is filed as an open question with
  a **kind**. Kinds are ranked most to least concerning:
  1. `excluded` (`not_kept`)
  2. `less` (`not_done`)
  3. `blocked` (`blocked`)
  4. `different` (`differently`)
  5. `more` (a grown item)
- **Accepted discrepancy**: one where the user's answer keeps the work as it
  is. It needs no flag: after the agent reconciles again, any entry that is
  still a discrepancy and has a settled question is accepted by
  construction.
- **Fingerprint**: what a reconciliation records about the code. It covers
  only the branch's own changes: the `git patch-id --stable` of the diff
  from the merge-base with the task's base branch to HEAD, with `.endless/`
  excluded. A clean rebase does not change it, and an edit does.
- **Earlier answer**: the settled question from a previous reconciliation
  on the same item. The agent, not the user, re-checks it against the whole
  task each time it reconciles again (item 9).

## Plan items

1. `task_plan_items` exists as a ledger-projected table, modelled on
   `task_questions`.
   - Columns: `id`, `task_id`, `seq` (never reused or renumbered),
     `sort_order`, `kind`, `statement`, `verify`,
     `detail`, `origin`, `state` (`active` or `withdrawn`),
     `approved_statement`, `approved_at`, and provenance.
   - It has schema DDL, a migration, events with exec, replay and projector
     wiring, and is listed in rebuild-loss accounting.
   - The list is flat. There is no nesting.
   - All database access is in Go.
2. `endless task plan add|edit|withdraw|move|list` manages items.
   `--items-file <toml>` (one `[[item]]` table per item) on `task add` and
   `task update` writes a whole set at once, and the format refuses anything
   it does not define.
3. `.endless/tasks/e-NNNN/items.toml` mirrors the items in the same TOML
   format and can be re-imported. The doc sweep repairs it like the other
   mirrors.
4. `task show --plan` renders the items first (P-number, kind, statement,
   verify), each with its latest verdict and the answer to its question,
   then the plan prose.
5. `task approve`, and `task update --status ready` from `submitted` (the
   same approval under another verb), copy each active item's statement and
   kind into `approved_statement` and `approved_kind`. Both, and `task spawn`,
   refuse a todo or bugfix task with no active deliverable item, and any task
   whose only active items are exclusions. Spawn refuses an already
   `ready` task with a prose-only plan too. `task claim` is not gated.
6. On a `ready` task, a plan edit is material only when the item set
   differs from the baseline: an item added, withdrawn, or with a changed
   statement or kind. Prose edits stop being material.
7. While `underway`:
   - a new item is `grown`;
   - an approved item can be edited or withdrawn by anyone, but every such
     change leaves the item set differing from the baseline, and the gate
     refuses until the user re-approves (Mike chose this over refusing the
     edit, so a redesign can be written by the agent and accepted in one
     place);
   - the user can re-approve, which re-baselines every item. Re-approval
     closes, as moot, every open discrepancy question on an item that is
     withdrawn, and marks the plan as changed for the next reconciliation.
   From `unverified` on, items and entries are frozen. `revisit` unfreezes
   them.
8. `task_questions` gains a `kind` column holding the five kinds above, and
   `question list` sorts by that rank.
9. `endless task reconcile <id> --file <toml>` records a reconciliation in a
   single event, made up of:
   - the fingerprint;
   - one entry per active item, with a verdict and a note;
   - one question series holding a question for every discrepancy that
     needs one, each with its kind.
   It refuses a missing entry, an entry for an unknown or withdrawn item,
   and a verdict that does not fit the item's kind.
   Earlier answers:
   - Before writing, `task reconcile` shows the agent each earlier answer,
     what has changed across the whole task since it (items added, withdrawn
     or with a new verdict, files changed, and whether the plan was
     re-approved), and the current items.
   - When the verdict is unchanged and the item has an earlier answer, the
     entry must say either `answer_holds = true` with a one-line reason, or
     `reask = true` with what changed. It refuses an entry that says neither.
     A kept answer files no question and is shown in `task show --plan`. A
     reask files a new question that shows the old answer.
   - A changed verdict always files a new question.
10. One gate function in Go passes only when all of these hold:
    - the latest reconciliation's fingerprint equals the current one;
    - every active item has exactly one entry;
    - every discrepancy has a settled question, meaning answered or closed
      in any way, by anyone, or an earlier answer kept with `answer_holds`;
    - the task has at least one active deliverable item;
    - the item set matches the approved baseline: no approved item has a
      changed statement or kind, and none has been withdrawn since approval.
      Grown items are not baseline differences; they are `more`
      discrepancies.
    There is no grandfathering by claim time.
11. The gate runs on any task with plan items, and on every todo and bugfix
    (which must have them), when the status is set to `unverified` or
    `assumed`, and inside `task verify` as a backstop. When the lifecycle
    refuses the move itself, the gate stands aside so that refusal is the one
    shown. The types that must be itemized are defined in one place, so adding
    docs means adding one entry.
    - A todo or bugfix cannot be created at `unverified` or a later status
      that reports its work (Mike, after review): `task.created` refuses it.
    - `worktree land`'s own move from `unlanded` to `assumed` skips the gate,
      because the verify it requires already ran it.
    - The backstop in `task verify` skips a task with no plan items at all.
      That only lets through tasks already at `unverified` when this ships,
      because setting `unverified` refuses a task with no items.
12. Relations:
    - E-2280 precedes E-2263, and E-2263's plan gains an item to call the
      shared gate from the verify request.
    - E-2280 relates to E-2147. E-2147 gets no plan items from this task.
      It is still unplanned, and the E-2276 session, with Mike's agreement,
      recorded both points (docs joining the gated types, and how an
      auto-landing docs task reconciles) in E-2147's analysis.
13. The top-level `endless plan` redirect ("renamed to task") is removed
    outright.
14. The guide's `docs/guide/tasks.md` is updated:
    - the `plan` row describes items plus approach prose, with rationale in
      `analysis`;
    - the terms above are defined;
    - the discovery rule's "do it, note it, say so in your reply" becomes
      "do it, and add it as a grown item; it is reported through the
      reconciliation";
    - the "As built" prose convention is removed.
15. The handoff templates (`_mechanics.tmpl`, `_close.tmpl` and the
    per-type templates) say:
    - reconcile after implementing;
    - if there are discrepancies, put them to the user one at a time in
      kind order, each with what differs, the options with pros and cons,
      and a recommendation, stopping after each so the user can ask about
      it; record each answer before the next (Mike, after review).
      `endless question list` shows them all at once;
    - when all are answered, make any changes, reconcile again, and only
      then set `unverified`.
16. Plan-time prompting asks for items through `--items-file`. That covers
    `prime.md.tmpl`, `task prime`, and the user-level `/needs-plan`
    command.
17. Tests cover:
    - projection and replay of items, reconciliations and question kinds;
    - each gate condition on its own;
    - a commit that touches only `.endless/` not making a reconciliation
      stale;
    - the approve, spawn and material-edit rules;
    - the CLI surfaces.
18. `src/endless/reconcile.py`, which syncs project rows for `endless scan`,
    is renamed `project_sync.py`, with its test file, so "reconcile" means
    only this task's reconciliation.
19. Last, after implementation: draft the decision recording the rule and
    these terms.
20. After Mike lands E-2280: message the active sessions, a few at a time
    and only when idle, waiting for each batch before the next. Each message
    says E-2280 has landed and what to do now (write plan items, reconcile
    before setting `unverified`), and to reply only with critical
    questions.

## Exclusions

- E1. Existing prose plans are not migrated automatically.
- E2. Research, brainstorm and epic tasks are not gated.
- E3. This task does not add the docs type (that is E-2147).

## Decided without asking

- Q2: docs goes through E-2147, as a one-entry type set.
- Q7: `assumed` is gated.
- Q8: items are shown with their verdict and answer inline.
- Q10: there is a TOML mirror.
- Q6: any settled question counts, as Mike chose.

## Answered (EQ-37 to EQ-43)

- EQ-37: the fingerprint covers only the branch's own changes.
- EQ-38: no grandfathering. The one exception is the `task verify` backstop
  for tasks with no items.
- EQ-39: a flat list, with no nesting.
- EQ-40: the agent re-checks earlier answers with `answer_holds` or `reask`,
  and re-approval closes moot questions.
- EQ-41: land's move to `assumed` skips the gate.
- EQ-42: spawn refuses `ready` tasks with prose-only plans.
- EQ-43: the `endless plan` redirect is removed.
- Item 7 (asked in chat): approved items may be edited while underway, and
  the gate holds until re-approval (option B).
