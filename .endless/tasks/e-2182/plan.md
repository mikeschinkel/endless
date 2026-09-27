# Stop parallel worktrees from colliding on migration numbers

## The problem

Migrations are numbered with the next free integer (00001, 00002, ...), and the
goose provider in `internal/schema/migrate.go` (`newProvider`) is built without
`WithAllowOutofOrder`. Two worktrees that each add a migration both take the
same next number. Today the only protection is a manual convention: whoever
lands second renames the file, and for a Go step also edits the version in
`migrations.Go()`. Nothing enforces it. E-2176 and E-1531 hit this back to back
(00004, then 00005), and parallel worktrees are the normal way Endless works,
so it will keep happening.

It fails in two ways:

- **Collision.** Two files with one version: goose refuses a duplicate at
  provider construction, which runs on every connect, so every `endless`
  command fails until someone renumbers.
- **Out-of-order landing.** A higher number lands before a lower one. Any
  database that already ran the higher one then refuses the lower one.
  `LatestVersion()` against `DBVersion` reports this as E-2020's "database is
  ahead of your binary", which reads as a real fault rather than a numbering
  mistake.

## Direction (proposed, not settled)

1. **Name new migrations by timestamp** (`YYYYMMDDHHMMSS_name.sql`, and the same
   number as the version of a Go step). goose already treats the numeric prefix
   as the version, so this needs no custom parser. Two worktrees then practically
   never pick the same name. 00001–00005 keep their numbers; timestamps sort
   after them.
2. **Build the provider with `goose.WithAllowOutofOrder(true)`**, so a migration
   that lands after a newer-timestamped one still applies.
3. **Rework the version-direction check.** With out-of-order allowed, "the DB's
   highest version exceeds the binary's highest" stops meaning "ahead". The
   check becomes set-based: the database holds an applied version the binary
   does not know (truly ahead), or the binary holds one the database has not
   applied (behind). Review `LatestVersion()`, its callers, and E-2020's
   comparison.
4. **Make it hard to get wrong.** A test that fails on a duplicate version
   across the `.sql` files and `migrations.Go()`, and on any new migration not
   using a timestamp. Plus a scaffold (e.g. `just new-migration <name>`) that
   writes the correctly named file, so nobody types a number by hand.
5. **Document the one rule it adds:** migrations from parallel branches can run
   in either order, so they must not depend on each other. A migration that does
   depend on another is expressed as a `blocked_by` between the two tasks.

## Alternative considered

Keep integers and have `worktree land` pick the next free number and rename the
file (and patch `migrations.Go()`) during the rebase. It keeps the numbers
tidy, but it rewrites source during a land, and `migrations.Go()` edits are code
changes a land should not be making. It also does nothing for the out-of-order
failure between two branches that have both landed.

## Acceptance

- Two worktrees can each add a migration and land in either order with no
  rename, and every database ends at the same shape.
- A duplicate version is caught by a test before land, never at connect.
- The ahead/behind check reports correctly under out-of-order application.
- `go test ./...`, `just test` and `just test-go` pass.
