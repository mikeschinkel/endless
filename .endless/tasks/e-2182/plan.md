# Brainstorm: how migrations should be named so parallel worktrees don't collide

## The spark

Migrations take the next free integer (00001, 00002, ...), and the goose
provider (`newProvider` in `internal/schema/migrate.go`) refuses both a
duplicate version and an out-of-order one. Two worktrees that each add a
migration take the same number. Today the only protection is a manual
convention: whoever lands second renames the file, and for a Go step also edits
the version in `migrations.Go()`. E-2176 and E-1531 hit it back to back. Since
parallel worktrees are how Endless works, this is a recurring cost, not a
one-off.

Two failures to design against:

- **Collision.** Two files with one version: goose refuses at provider
  construction, which runs on every connect, so every `endless` command fails.
- **Out-of-order landing.** A higher number lands before a lower one; any
  database that ran the higher one then refuses the lower. `LatestVersion()`
  against `DBVersion` reports it as E-2020's "database is ahead of your binary",
  which reads as a real fault.

## Approaches to hash out

### A. Timestamp names + allow out-of-order

New migrations are named `YYYYMMDDHHMMSS_name`; the provider gets
`WithAllowOutofOrder(true)`.

- Pro: no coordination at all; collision is practically impossible.
- Pro: goose supports it natively; no custom code in the land path.
- Con: parallel migrations may run in different orders on different databases,
  so they must be independent, and nothing checks that they are.
- Con: the "ahead of your binary" check must change from comparing highest
  versions to comparing applied sets.
- Con: two numbering styles coexist (00001–00005, then timestamps).

### B. Land renumbers

Keep integers; `worktree land` picks the next free number after rebasing and
renames the file (and patches `migrations.Go()` for a Go step).

- Pro: numbers stay dense and read as a clean sequence.
- Pro: strict ordering stays; no independence rule needed.
- Con: the land rewrites source, including Go code, after review. What lands is
  not exactly what was verified.
- Con: the other branch still holding the old number keeps the problem until it
  rebases.
- Con: more logic in the land path, which is already the riskiest code.

### C. Reserve a number when the migration is created

A scaffold command claims the next number in a shared place (the main DB, or a
file committed to main) the moment a worktree creates a migration.

- Pro: dense integers without renaming at land.
- Con: a reserved number whose task is abandoned leaves a gap, and gaps trip the
  out-of-order refusal unless it's also relaxed.
- Con: needs a shared counter and a commit to main outside the task branch.

### D. Detect only, never fix automatically

Keep integers and the manual rename, but add a pre-land check that fails loudly
on a duplicate or out-of-order version and names the rename to do.

- Pro: smallest change; nothing clever.
- Con: still a manual step every time; it only makes the failure clear.

### E. Allow out-of-order, keep integers

Turn on `WithAllowOutofOrder` but keep integer names.

- Pro: fixes the out-of-order failure with one line.
- Con: does nothing for collisions, which are the common case.

## Questions to work through

- How often do parallel migrations actually depend on each other? If almost
  never, A's independence rule costs little; if often, strict order (B, C)
  matters.
- Is "what landed is exactly what was verified" a hard rule? If so, B is out.
- Does a dense, human-readable sequence matter to anyone, or only to goose?
- PRODUCT: a downstream project using Endless has its own migrations (or none).
  Does this choice leak into what Endless expects of them?
- Whatever wins, should a test catch duplicate versions across the `.sql`
  files and `migrations.Go()` before land? (Likely yes under every option.)
