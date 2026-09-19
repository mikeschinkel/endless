# E-2047 — supersede must record why, and must not leave contradictory links

## 1. The missing reason

### Why it shipped without one

Not an oversight. E-1920 argued the asymmetry explicitly: `obsolete` requires
`--reason` because "an accepted decision governed something; retiring it without
saying what went away leaves exactly the unanswerable 'is this still in force?'
gap", while supersede was held to need none because "the replacement decision IS
the reason." That second claim is false whenever the cause of retirement is a
fact about the world rather than about the successor. ED-1067 is the case: it
was superseded because both of its consumers (E-995, E-1039) died, which ED-1570
does not state and cannot be inferred from.

The first argument applies verbatim to supersede. The asymmetry was rationalized,
not reasoned, which is why it survived review.

### Storage

Do NOT add a third reason column. There are now three ways a decision ends and
two distinct meanings:

- `rejection_reason` — why it was never adopted. Distinct; keep.
- retirement reason — why an adopted decision stopped governing. Shared by
  `obsolete` and `superseded`.

Rename `obsolete_reason` to `retire_reason` (SQLite supports RENAME COLUMN;
change file under `internal/schema/changes/`, backfill is identity). Update
`execDecisionObsoleted`, `execDecisionReinstated`, the replay pair, and the
three renderers in `decision_cmd`.

### Required (user decision)

`--reason` is REQUIRED on supersede, matching `obsolete` and `reject`. The
"optional, because the successor usually explains it" position is what produced
this task; the common case being adequate is not a reason to make the load-
bearing case unrepresentable.

## 1b. Reversals must not destroy the reason silently

`unreject` clears `rejection_reason` (E-1864); `reinstate` clears
`obsolete_reason` (E-1920, copied from it). Both are silent, on the common path,
unrecoverable. That violates the standing principle that Endless does not
destroy data on the common path.

**Decision: `--force`.** Both verbs refuse when a reason is stored, naming the
text that would be lost and requiring `--force` to proceed. The user gets the
chance to capture it first, and destruction becomes deliberate.

**Rejected: a `decision_transitions` table.** Storing the reason per transition
instead of on the row is the correct design — the reason belongs to the status
change, not to the decision, and an append can never leave an invalid row. It is
rejected on timing, not on merit: E-1868 moves decisions back onto tasks, and
building a decisions-side history table weeks before the table it hangs off is
removed buys a property twice. Revisit the transition model as part of E-1868,
where it applies to tasks and decisions alike.

**Also rejected: keeping the value and gating the display on status.** That
leaves the row internally inconsistent and correct only to a renderer that knows
the rule — moving the defect somewhere harder to see rather than fixing it.

## 2. Redundant links

`supersede` calls `link_decision`, which pre-checks only the exact
(source, target, type) tuple. An existing `reverses` or `modifies` row between
the same pair is invisible, so ED-1067 renders `← ED-1570 (reverses)` and
`← ED-1570 (supersedes)` as two independent facts.

They are not independent. `reverses` and `modifies` describe CONTENT; only
`supersedes` carries a status. Once superseded, the content relation is a detail
of the supersession, not a peer.

Approach: on supersede, detect an existing `reverses`/`modifies` row for the
pair and refuse with the choice spelled out — keep both (the contradiction is
real: it reverses AND supersedes), or drop the content link. Refuse rather than
auto-drop: silently deleting a relation a human recorded is worse than one
render nobody can interpret. `decision show` should also group a content link
under the supersession rather than listing it as a sibling.

## 3. Data already affected

ED-1067 carries both links and no reason. Cleanup is the other session's call,
not this task's: `decision unlink ED-1570 --to ED-1067 --type reverses`, and the
reason cannot be recovered — supersede did not record it and `unreject` had
already cleared `rejection_reason`. Worth noting in the outcome as the concrete
loss this task prevents recurring.

## Verification

- `supersede` REFUSES without `--reason`, and refuses an empty one.
- `--reason` is stored and rendered by `decision show`, `--llm` and `--json`.
- `obsolete` still refuses an empty reason.
- `unreject` on a decision with a stored reason REFUSES, quotes the text that
  would be lost, and names `--force`.
- `unreject --force` clears it and says so.
- `unreject` on a decision with no stored reason needs no flag.
- Same three for `reinstate` and `retire_reason`.
- The rename leaves existing obsolete reasons readable.
- Superseding a pair that already has `reverses` refuses and names both options.
- Round trip: supersede with reason, reinstate, supersede again.

## Not in scope

Whether `reverses`/`modifies` should survive E-1868's move of decisions back
onto tasks. This task assumes the current decisions table.
