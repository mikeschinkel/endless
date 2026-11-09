# Measure the branch's own work without context lines

From the E-2577 brainstorm. The reconciliation fingerprint is `git patch-id`
of the branch's diff against its merge-base. patch-id hashes context lines, so
a clean rebase onto a main that changed lines near the branch's edits changes
the fingerprint with no change to the branch's work. E-2486 re-recorded
identical verdicts three times (R3 to R5) for that reason.

## Changes

- One Go function computes the measure: a hash of the branch's added and
  removed lines per file, against its merge-base with main, context excluded,
  `.endless/` excluded. `git patch-id` cannot do this, so it is a small routine.
- The reconciliation gate uses it in place of patch-id.
- The sibling task that computes whether a pass stands (E-2583) uses the same
  function, so "the branch's own work changed" means one thing everywhere.
- Existing reconciliations store a patch-id. Recompute the new measure for each
  at its recorded commit, or treat a stored patch-id as current once on first
  read; pick whichever the stored commit makes reliable.
- E-2479's re-reconcile at `unverified` stays for the cases this leaves: a real
  change to the branch's lines.

## Verify

A suite proving: a clean rebase with context drift leaves the measure equal;
a change to an added line changes it; a commit touching only `.endless/`
leaves it equal; the gate accepts a reconciliation across such a rebase.
