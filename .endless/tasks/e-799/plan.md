# Event-Sourcing Architecture: Implementation Plan

## Key Design Decisions (2026-04-24)

1. **Kairos** — HLC serialization format lives in `internal/kairos/`, to be extracted as standalone package later. Not "HLC library" — a serialization format backed by hybrid logical clocks.

2. **Go subprocess, not duplicate** — Python CLI shells out to Go for all event writes. Single implementation of the critical write path. Enables sqlc from day one. No format divergence risk.

3. **Segmented event files** — Each machine writes to its own segment: `.endless/events/events-{node_id}-{seq}.jsonl`. Git merge adds files, never line-level conflicts. Replay merge-sorts across all segments by kairos timestamp.

4. **~/.config/endless/ as git repo** — No separate changelog repo. Cross-project events live here. `.gitignore` excludes `endless.db` and local cache. Multi-machine sync via git.

5. **Local-only compaction** — `state.jsonl` is `.gitignored`, local cache only. Git-committed segment files are never modified after rotation. Zero merge conflicts by design.

## Critical Path

```
E-803 (kairos package)
  → E-804 (event envelope + vocabulary)
    → E-805 (Go event-writing CLI + dual-write)
      ├→ E-806 (~/.config/endless/ as git repo)
      └→ E-807 (projection engine + rebuild-db)
          → E-808 (events-authoritative write path)
            → E-809 (local-only compaction)
```

## Stage 1: Dual-Write

- **E-803**: Kairos package in `internal/kairos/`. ~50 lines core. Lexicographic sort = causal sort.
- **E-804**: Event envelope spec + closed vocabulary. Segmented file format.
- **E-805**: Go CLI subcommands for event writes. Python shells out. Segment rotation at threshold.
- **E-806**: `~/.config/endless/` becomes optional git repo for cross-project state.

## Stage 2: Projection

- **E-807**: Go projection engine. Reads all segments across all projects, merge-sorts by kairos, builds SQLite. `endless rebuild-db` command. Validate projection matches current DB.

## Stage 3: Events-Authoritative

- **E-808**: Writes go through Go event subcommands first. Projection updates SQLite as side effect. DB is a cache.
- **E-809**: Local-only compaction. `endless compact` merges old segments into `.gitignored` state.jsonl.

## Stage 4: Supporting Features (independent, any order)

- **E-810**: Lexorank ordering
- **E-811**: Text field split (plan/acceptance/design/notes)
- **E-812**: Dependency model
- **E-813**: Close-time gates
- **E-814**: Atomic claim primitive
- **E-815**: Conflict surfacing
- **E-816**: Paired identifiers
- **E-817**: Hook adaptor layer + E-716 (Go distribution)
