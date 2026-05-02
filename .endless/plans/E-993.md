# Resolution: Declined 2026-04-29 (session 249)

The premise of this task was wrong. The E-979 desync I created during E-969 verification was not caused by a missing `task clear text` verb. It was caused by running verification mutations directly against the live event log + DB and then needing to undo them.

Adding a CLI verb that makes "mutate live then reset" easier would normalize the wrong workflow — agents would reach for it whenever a verification run leaves crud behind, instead of reaching for an isolated test environment.

## What was actually wrong

Two unstated assumptions in the original framing, both of which need to be answered before the "missing verb" question is even meaningful:

1. **Should writes ever bypass the event log to avoid having to redo work?** No. The event log is authoritative; the DB is a projection. The moment a "small" bypass is allowed, `rebuild-db`'s correctness guarantee is destroyed because real state lives outside the log. There is no principled line between "OK bypass" and "not OK bypass."

2. **Should tests run against live data and then clean up?** No. Tests and verification work that mutate state belong in an isolated sandbox/fixture (separate event log + separate DB). Cleanup of live state is the wrong shape — it creates exactly the temptation that produced the E-979 desync in the first place.

## Replacement work

E-995 — Design sandbox/fixture pattern for tests that mutate state. Big task, needs its own planning session before implementation.

A `task clear text` / `task clear prompt` verb may still be filed later on its own merits (legitimate user need to null out a field), but it must wait until E-995 lands. Giving agents a clear verb before sandboxes exist normalizes "mutate live then reset," which is the exact anti-pattern that triggered this whole thread.
