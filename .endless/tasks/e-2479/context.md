When a task's branch changes after it was set to `unverified` (here, a rebase to resolve a land conflict), `endless task verify E-N` refuses with only:

    E-2418 is unverified again: task/2418 has changed since the verify passed at 919d498817.
    Error: E-2418 cannot be verified yet:
      - the code changed since the last reconciliation; reconcile again

The text comes from internal/reconcilegate/reconcilegate.go (`fail("the code changed since the last reconciliation; reconcile again")`). It names neither why the code changed nor how to reconcile, and "reconcile again" does not work as written. Recovering E-2418 took an agent five refused steps, each discovered only by hitting the next refusal:

1. `endless task reconcile E-N --file <toml>` refused: "E-N is unverified, so it can no longer be reconciled; reopen it with --status revisit first" (internal/events/plan_items.go).
2. `endless task update E-N --status revisit`.
3. Reconcile refused again: every item with an earlier answered discrepancy needs `answer_holds = true` (with a `reason`) or `reask = true` in the file.
4. Reconcile recorded R2.
5. `endless task update E-N --status unverified` refused: not legal from revisit; it has to go revisit → underway → unverified.

Only then did verify run. A user running verify by hand, as here, is given none of this.
