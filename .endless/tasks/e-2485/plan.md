# Require plan approval before a task reports its work

Approval of a task's plan items has to happen before the task is handed in as
done. Once a task is `unverified`, its items can no longer change, approval
included, so an approval skipped before that point can't be given afterwards.
Requiring it at the hand-in closes that path.

## Approach

The check goes in the reconciliation gate (`internal/reconcilegate`, `check`),
which already runs on every hand-in: an agent setting `unverified`, or setting
`assumed` directly. It applies in handover mode only, not in the backstop that
`endless task verify` runs.

- Today the gate compares items to the user's approval only when at least one
  item was ever approved (the `baselined` flag). The new check doesn't depend
  on that: any active item with origin `planned` and no approval as it stands
  fails the hand-in.
- Grown items are left to the existing discrepancy check, which already
  requires the user's answer to their reconciliation question.
- The refusal names the unapproved items and the approve command, the same
  wording `plan_items.require_items_approved` gives for `task approve`. The
  task is still `underway`, where approving is allowed, so the user can approve
  and the agent can retry.

## Tests

In `internal/reconcilegate`'s tests:
- a task whose only items were added while `underway`, none approved, is
  refused at the hand-in to `unverified` and to `assumed`;
- approving them lets it pass;
- grown items kept through an answered question pass;
- the backstop mode does not refuse the same task.
