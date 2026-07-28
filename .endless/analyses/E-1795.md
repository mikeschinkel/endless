# Why this, and the deliberate choices

## Direction asymmetry: blockers walk the chain, children do not
`session status` today (internal/monitor/session_status.go → SessionStatusRows)
walks `dep_type='blocks'` in the DOWNSTREAM direction only — the focal's direct
dependents (one hop, E-1685) plus direct children (E-1691). Both are capped at
one hop to avoid exploding an epic's subtree into the pane.

This task adds the UPSTREAM walk (`blocked_by`) and makes it TRANSITIVE, for
every task already in the view — not just the focal. The asymmetry is
deliberate: children fan out (an epic can have dozens), so a transitive child
walk would flood the pane; blocker chains are narrow (each task has few
prerequisites), so walking the full chain stays bounded in practice.

## Escape valve (deferred — NOT built here)
If a specific blocker chain does get noisy, the intended fix is a command to
HIDE a given task from session status — an edge-case release valve — rather
than pre-capping blocker depth. Deferred until the problem is actually felt;
filing/building it now would be speculative.

## Why it's worth doing: makes coarse / idealistic blocks visible
An upstream block that is too coarse (a task dragged behind a whole epic when it
needs only a sliver) or merely idealistic (ideally-first, not literally
required) currently drags work SILENTLY — the gate is never shown. Surfacing the
upstream chain is what makes such blocks visible so they can be corrected. This
does NOT resolve the over-blocking problem itself (that is E-1708); it exposes
it. Related axis already in the model: hard block = `blocks`, soft/advisory =
`relates_to`; E-876 set the status-based release (verify blocks, assumed
unblocks).

## Concrete motivating case
E-1671 (upcasting-pipeline epic, unplanned) is the head of the chain
E-1671 → E-1659 → E-1658 → E-1536. None of it surfaces in this session even
though E-1536 (a direct dependent of the focal E-1533) IS shown — because the
view never walks upstream. That gap — losing sight of the real front of the
line — is what this task closes.
