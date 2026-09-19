# Analyze recent session handoffs → propose the `task report --xml` element set

Grounds the schema for E-1771's `--xml` payload in what agents *actually* report
at handoff, rather than inventing it. "XML schema" here is used conveniently,
not normatively: the deliverable is the set of **elements** (and any
**attributes**) needed to represent a real handoff/report — not a formal XSD.

## Method
1. Locate ~25 recent sessions whose final agent message is a handoff or a
   status/end-of-session report (session transcripts / final assistant
   messages; skip sessions with no reporting turn).
2. For each, extract the report and **classify the concerns** it raised — the
   distinct *kinds* of information the agent conveyed (e.g. out-of-band git
   anomaly, how-to-verify, return line, follow-ups spawned, external-service
   state, blocked-on, decisions recorded, files touched outside the repo, …).
3. Aggregate the concern taxonomy across all sessions: which concerns recur,
   which are one-offs, which are pure ceremony (the absence of a problem).

## Deliverable (in the task outcome)
A **proposed element/attribute set** for `--xml`, mapped to the observed concern
categories, that partitions each concern into one of:
- **Computed** — comes from handoff-status (E-1758) / other tooling; the agent
  never supplies it (no element, or an auto-filled one).
- **Validated-value element** — a constrained value set with `none` allowed
  (rendered as silence); out-of-set values rejected.
- **Free-text element** — the genuinely non-computable escape hatch (e.g.
  out-of-band anomalies) guarded by the scoped per-entry check.

Call out, per element: whether it repeats, whether it needs an attribute (e.g. a
kind/severity), and which ceremony categories the schema deliberately gives
**no** element to represent (that is the point — unrepresentable, not detected).

Recommend the schema; E-1771 consumes it.
