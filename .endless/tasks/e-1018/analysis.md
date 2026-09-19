Surfaced during E-987 design discussion (2026-04-30). Tomorrow's problem; not blocking E-987 or E-971.

## The question

Two patterns for the projection engine (E-807) when the events log grows:

**Pattern A: Always rebuild from full event log.**
- '.endless/data.sql' is unnecessary; events log is sole source of truth.
- 'endless rebuild-db' replays every event from the beginning.
- Performance scales linearly with event count.
- For solo dev with manageable event-log size, fine for years.
- Simpler architecture: one source of truth, no checkpoint logic.

**Pattern B: Periodic data.sql checkpoint with event-log truncation.**
- 'endless dump' (or similar) creates a checkpoint at .endless/data.sql, then truncates the events log (or starts a new segment, leaving older ones removable).
- Rebuild = checkpoint + remaining events.
- Performance bounded by checkpoint frequency.
- More complex: checkpoint creation, segment retention rules, rebuild-with-checkpoint logic.

## Current state

data.sql exists in the repo (~114KB on 2026-04-30, last updated 2026-04-21 — 9 days stale). It's a periodic dump created by an explicit command, not auto-updated. So we're loosely in Pattern B without the truncation half being implemented.

## What to decide later

1. Is data.sql actually load-bearing (anyone depends on it being a checkpoint) or vestigial (could be deleted today)?
2. What's the events-log growth rate in practice? Measure across a few months to know if Pattern A scales.
3. If Pattern B: how often to checkpoint? What's the segment retention policy? Who triggers it (manual command, hook, daemon)?

## When to revisit

- If 'rebuild-db' becomes painfully slow.
- If events log grows past some threshold (e.g., 100MB, or rebuild > 30s).
- If a fresh-clone workflow needs faster bootstrap than full replay.