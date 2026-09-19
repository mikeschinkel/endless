# E-894 Phase 3 — Go owns DB creation

Umbrella context: `endless task show E-894 --text`. Blocked by Phase 2.

## Goal

Python stops creating and migrating the schema. Go's schema file, applied when
Go opens the DB, becomes the sole owner of DB creation. This subsumes the
empty-DB crash filed as E-1116.

**No migration logic is written.** The forward schema already lives on the Go
side (E-1459); there is nothing to port. This is a subtractive change.

## Scope, stated as a boundary

This removes the schema **bootstrap**, not all Python SQLite access. After Phase
2 Python no longer reads task tables for display, but it still reads other
tables. The Python query helpers and connection handling stay; only
schema creation and migration go.

"Python has zero SQLite knowledge" is true for task display at the end of E-894.
It is not true for every table — that is E-1486's family.

## The one real design question

Most entry points are Python. Once Python no longer creates the schema, a
brand-new DB has to be created by Go before any Python query opens it, or the
first command on a cold install fails on a missing table.

Resolve this at implementation. Either Python ensures the DB via a cheap Go call
when the file is missing or empty, or a Go path is guaranteed to run first on
install. Pick the simplest thing that keeps a cold config directory working end
to end. No backward-compatibility shims — this is unshipped software.

## Verification

- A cold, empty config directory: the first command creates the DB through Go
  alone, and E-1116's missing-table error does not reproduce.
- No schema-creation or migration machinery remains on the Python side.
- Tests pass against an empty config dir.
- Full smoke over a cold install: add, show, list, next, search, confirm, and a
  session flow.

## Closeout

This is the terminal phase of E-894, and Endless does not roll parent status up
from children. Once this lands and the other two phases are confirmed, move
E-894 to `unverified` so Mike can confirm it. Do not leave the umbrella sitting
at `ready` with all its phases done.
