# Systematic backlog cull

## Why this exists

Measured over the sixty days before this task was filed: 391 tasks filed
against 264 resolved (landed plus killed). That is roughly 1.5 filed for every
one closed; over the project's life it is closer to 1.9. Any ratio above 1.0
means the backlog diverges and the project has no completion date, no matter
how fast work proceeds — working harder raises both sides of the ratio.

Near 1.0 the sensitivity is severe. Against the open count at filing time,
a sustained ratio of 0.5 clears the backlog in roughly nine months; 0.9 takes
over three years; 1.0 leaves it permanently where it is.

Filing discipline going forward is necessary but slow-acting. This task is the
fast lever: the open backlog itself.

## What to do

Sweep every open task and assign one of four dispositions. Close or park
aggressively; the bar for keeping a task open is that someone would genuinely
be worse off if it vanished.

- **obsolete** — superseded, or aimed at code or a design that no longer
  exists. Record why in the outcome, not just the status.
- **declined** — understood and deliberately not doing it. Requires a reason.
- **maybe** — plausibly wanted someday, nothing to act on now. Parking is not
  a defeat; it is what the phase is for.
- **keep** — leave open, and be able to say what makes it worth carrying.

Phase has stopped discriminating: at filing time the great majority of open
tasks sat in `now`, which makes the field carry no information. Re-phasing is
part of the sweep, not a separate pass.

## Hard constraint

**File nothing.** This task closes and parks; it does not open. A discovery
made mid-sweep belongs in an existing task's analysis, or in the report to the
user at the end — never in a new task. A sweep that files as it goes defeats
its own purpose.

Batch related tasks under one shared outcome where a group dies for one
reason; that is both faster and produces a better record than per-task prose.

## Suggested approach

Work in coherent groups rather than by ID order — by subsystem, by originating
epic, or by the design they assumed. A group that shares a premise usually
shares a fate, and judging it once is more accurate than judging its members
separately.

Prior art worth reading before starting: the sweep that obsoleted the
documents epic and the web dashboard subtree, recorded on E-801 and E-1939.
Both killed large coherent groups on a single stated premise.

## Verification

Report the before and after open counts, the split across dispositions, and
the resulting file-to-close ratio for the sweep itself. The sweep succeeds if
the open count falls materially and no new tasks were created.
