Decisions already made this call (E-1920, decision obsolete --reason).

Fix by widening the existing _require_outcome_for_declined guard to cover obsolete, which extends it to all three call sites including task replace, whose replaced task defaults to obsolete — the relation records WHAT replaced a task, not WHY.

Store in outcome; no schema change; do not backfill.
