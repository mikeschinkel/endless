# Brainstorm outcome: `complexity` + `risk` axes for auto-spawn eligibility

## Goal

Give Endless a lever so the system can **initiate a spawn on its own** for tasks
that are safe to work without the user explicitly running `endless task spawn`.

Endless never works a task headless — an auto-initiated spawn still opens a
first-class, interactive session (openable in a tmux window) that the user can
jump into. The autonomy is only in the **selection + spawn trigger**, not in
running work unattended.

Origin: sparked by Matt Pocock's video "Do you even need a human review?", but
the review-timing / shift-left-vs-right framing from that video was set aside as
at most a later tangent. The lever wanted here is auto-spawn *selection*, not
pre-vs-post review.

## Two axes (both new `tasks` columns)

- **complexity** — how much human-AI interaction is needed to nail down the
  specifics. High complexity is nearly the definition of "cannot be worked
  unattended," so it gates auto-spawn by meaning, not by a bolted-on rule.
- **risk** — blast radius if the change is wrong. A low-effort change can still
  be high-risk (e.g. flipping a data-deleting flag), so risk must be its own
  axis or the gate misses exactly the dangerous case.

**effort** was considered and **dropped for now** (roughly Scrum story points).
It does not gate auto-spawn — mid-effort work is fine to auto-spawn. May return
later as a third axis.

## Representation

- User-facing named levels: `low` / `medium` / `high`, same vocabulary for both
  axes, so no one has to remember which numeric end means what.
- Behind the scenes: `low=1, medium=3, high=5`. Values **2 and 4 are left
  unseeded on purpose** so `medium-low` / `medium-high` can be inserted between
  existing levels later with no renumbering. Gapped spacing is the whole reason.
- Storage: each axis is an enum, so per the no-CHECK-constraint convention it is
  an **integer FK column referencing its own seeded values table** —
  `complexity_levels` and `risk_levels`. **Two separate tables**, not one shared:
  matches how `status` and `phase` each get their own table, and the two
  vocabularies are expected to drift (risk may later gain `critical`; complexity
  may gain `medium-low`).
- Go side: int-const (no iota) + `String()` slug + `Parse()` enum pattern.
- Both columns **nullable**; NULL means unevaluated.

## Who sets it

- The agent **proposes** complexity + risk when it submits a task (alongside the
  plan-attach or description-sufficient submit).
- **The user ratifies** at `approve`. This reuses the existing submit -> approve
  gate as the human ownership point, so an agent cannot relax its own auto-spawn
  gate — no self-granted bypass.

## How it is consumed

- Used only at spawn-decision time.
- Eligibility is **computed, not stored** (compute over cache):
  `auto_spawn_eligible = complexity <= T_c AND risk <= T_r`
- Initial thresholds: `T_c = low`, `T_r = low` — only the safest tasks
  auto-spawn. Adjust as needed; move thresholds to config later.
- Auto-spawn only ever touches `ready` tasks, and `ready` exists only after
  approval, so "must be set before spawn" is automatically satisfied. Re-rating
  a task afterward is allowed and simply re-computes eligibility (nothing
  cached to invalidate).

## Scope notes

- **Epics / container tasks:** value is NULL. An epic's complexity is just an
  emergent read of its leaf do-tasks; auto-spawn operates on leaves.
- **Orthogonal to `type`:** a bug can be high-complexity. These axes are
  independent of task type.
- Filtering/listing by these values is assumed a given, not the driving use —
  the driving use is the auto-spawn decision.

## Still open / deferred

- Threshold values eventually move to config (start hardcoded at low/low).
- `effort` / story-points as a possible future third axis.
- Names for the reserved `2` / `4` levels when first needed
  (`medium-low` / `medium-high` as working placeholders).

## Next step

File an implementation task to add: the two nullable FK columns; the two seeded
values tables; the Go enums; the propose-at-submit / ratify-at-approve wiring;
and the auto-spawn eligibility computation plus the spawn trigger.
