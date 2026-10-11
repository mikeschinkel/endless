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
  (`origin = grown`). It is work done beyond the approved plan.
- **Reconciliation**: the agent's record, made after implementing and before
  handing over a verify, of how the work compares with every active plan
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

## Plan items

1. `task_plan_items` exists as a ledger-projected table, modelled on
   `task_questions`.
   - Columns: `id`, `task_id`, `seq` (never reused or renumbered),
     `parent_seq` (one level), `sort_order`, `kind`, `statement`, `verify`,
     `detail`, `origin`, `state` (`active` or `withdrawn`),
     `approved_statement`, `approved_at`, and provenance.
   - It has schema DDL, a migration, events with exec, replay and projector
     wiring, and is listed in rebuild-loss accounting.
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
5. `task approve` copies each active item's statement into
   `approved_statement`. `task approve` and `task spawn` refuse a todo or
   bugfix task with no active deliverable item. `task claim` is not gated.
6. On a `ready` task, a plan edit is material only when the item set
   differs from the baseline: an item added, withdrawn, or with a changed
   statement or kind. Prose edits stop being material.
7. While `underway`:
   - a new item is `grown`;
   - an approved statement cannot be edited;
   - withdrawing an approved item is refused;
   - the user can re-approve, which re-baselines every item.
   From `unverified` on, items and entries are frozen. `revisit` unfreezes
   them.
8. `task_questions` gains a `kind` column holding the five kinds above, and
   `question list` sorts by that rank.
9. `endless task reconcile <id> --file <toml>` records a reconciliation in a
   single event, made up of:
   - the tree of the branch's HEAD, with `.endless/` excluded;
   - one entry per active item;
   - one question series holding a question for every discrepancy, each
     with its kind.
   It refuses a missing entry, an entry for an unknown or withdrawn item,
   and a verdict that does not fit the item's kind.
10. One gate function in Go passes only when all of these hold:
    - the latest reconciliation's tree equals the current one;
    - every active item has exactly one entry, with a parent covered by its
      children;
    - every discrepancy's question is settled, meaning answered or closed in
      any way, by anyone;
    - the task has at least one active deliverable item.
    A task claimed before this ships, and with no items, is skipped. That is
    decided by the claim event's time.
11. The gate runs on todo and bugfix tasks when the status is set to
    `unverified` or `assumed`, and inside `task verify` as a backstop. Those
    types are defined in one place, so adding docs means adding one entry.
12. Relations:
    - E-2280 precedes E-2263, and E-2263's plan gains an item to call the
      shared gate from the verify request.
    - E-2280 relates to E-2147, and E-2147's plan gains two items: add docs
      to the gated types, and settle how an auto-landing docs task
      reconciles.
13. `plan` is no longer an alias for `task`.
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
    - if there are discrepancies, list them in kind order as the final
      message and stop;
    - after the user answers, record the answers, make any changes,
      reconcile again, and only then set `unverified` and hand over the
      verify.
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
18. Last, after implementation: draft the decision recording the rule and
    these terms.

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
