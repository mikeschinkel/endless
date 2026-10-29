Running `endless task verify E-2419` after E-2419 landed (status `assumed`, branch HEAD a1cfd49 on main) refused with:

    Error: E-2419 cannot be verified yet:
      - the code changed since the last reconciliation; reconcile again

The message is false. The reconciliation gate (internal/reconcilegate, `Fingerprint`) fingerprints the branch as the patch-id of its diff from the merge-base with main to HEAD. Once the branch is in main, the merge-base is HEAD, the diff is empty, and the fingerprint is "empty" — which never matches the one stored at reconciliation. So every landed task gets this error, and reconciling again cannot clear it.

Verifying a landed task should not run at all: a suite is a land-time gate (.endless/tasks/CLAUDE.md), and a pass would call `unlanded.record_pass` (src/endless/verify_cmd.py), moving the task's state on a run that means nothing.
