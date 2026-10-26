# Plan

Terms:
- **post-land item** (ED-1636): a plan item whose work needs the landed, migrated, locally installed result and produces no code.
- **open post-land item**: an active post-land item with no post-land verdict yet, or whose latest post-land verdict is a discrepancy with an open question.
- **shipped** (ED-1638): a task's work has landed at least once or, for a research or brainstorm task, its outcome has been accepted (`completed`).

## Approach

1. **Mark.** Plan items get a `post_land` flag, for deliverable items only. It is set:
   - with `post_land = true` in an items file, or with `--post-land` on `task plan add` and `task plan edit`;
   - or by the user's `question answer EQ-n --post-land` on an open `blocked` question filed for an item. That marks the item and counts as the user's approval of the mark.
   A migration adds the column; the event payloads carry it.
2. **Pre-land verdict.** A new deliverable verdict, `deferred`, is valid only on a `post_land` item. It is not a discrepancy, so it files no question and the reconcile gate passes it. An unmarked item that can't be done before the land is reconciled `blocked`, as today.
3. **Status `landed`.** Add it to `internal/taskstatus` (the transition table, groups, labels and glyph) and run `just lifecycle-index`. Transitions:
   - `unlanded → landed`: user, "lands the work with post-land items open";
   - `landed → assumed`: system, on the post-land reconcile or withdrawal that leaves no open post-land item;
   - `landed → revisit | declined | obsolete | superseded`: user.
   No transition to `confirmed`, and no agent `assume`. `worktree land` (including `--record-only`) sets `landed` instead of `assumed` when any active `post_land` item exists; `--keep-status` is unchanged.
4. **Post-land reconcile.** `task reconcile` on a `landed` task:
   - prints and accepts entries for `post_land` items only;
   - records them as a new event kind;
   - writes one row per item to a new table keyed to the reconciliation that was in force at the land. Earlier reconciliations are untouched and nothing is copied.
   Discrepancies file questions, and `answer_holds`/`reask` work against the item's previous post-land row. The task stays `landed` while any post-land item is open.
5. **Gate.** Reconcile on a `landed` task is refused when the branch fingerprint is not `empty`, meaning the branch has changes outside `.endless/` since the land. The refusal names the remedy: file a follow-up task with `--cleans-up`.
6. **Withdrawal.** The user, and only the user, may withdraw a `post_land` item on a `landed` task.
7. **Readers.** An item's current verdict is its newest post-land row if one exists, otherwise its entry in the latest reconciliation. Readers: `monitor/plan_items.go`, the reconcile gate, `task plan list`, the reconcile context display.
8. **Blocking.** A `landed` task still blocks its dependents.
9. **Shipped.**
   - Rename the existing `Shipped` status group (slug `shipped`) to `Reported` (slug `reported`), and `ShippedTerminal` to `ReportedTerminal`.
   - Relabel the ⏚ action from "landed" to "shipped", keeping the glyph. It applies to a task that has landed at least once, and to a `completed` research or brainstorm task.
   - Give the `landed` status its own action and glyph, ranked above ⏚ in `taskrow.Classify`.
   - Text that uses "shipped" for task state follows the definition above.
   - Delegate the audit of the roughly 160 uses of "shipped" to a subagent. It classifies each as task state (rewrite to fit ED-1638) or ordinary English (leave it), and returns the list for the implementing session to apply.
10. **Surfaces.**
    - Reconcile output when a `deferred` verdict is recorded: says the land will set `landed`, and names the post-land reconcile to run then.
    - A post-land discrepancy says the task stays `landed` until the item settles.
    - `worktree land` lists the open post-land items when it sets `landed`.
    - The per-prompt hook context, `task show` and `task list` show a `landed` task's open post-land items.
    - `task active` includes `landed`.
    - The guide subsection `endless guide tasks/post-land` (E-2443) holds the rules, linked from `tasks`; the messages above point to it. The index's status and blocking text covers `landed`.

Proposed glyphs, for you to object to:
- status glyph `⇣` (landed, more to do after the land; the pair to `⇥` unlanded);
- action `⊡` "post-land".
Both must pass the single-width tests.
