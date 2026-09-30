Benefits: undo via CLI, cross-references survive (E-1219's outcome references E-1237 — would not have broken under soft delete), audit trail preserved.

Costs: every query needs WHERE status != 'deleted' (or a view), increased row count, FK-cascade semantics need rethinking.

Audit-then-decide;

covers tasks at minimum, possibly other deletable rows.
