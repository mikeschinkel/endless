Close the gap where the main checkout's endless-go is older than the database it reads. Today land migrates (step 5.5), then spends seconds compiling the new binary (step 5.6). The tmux status line polls about once a second through bin/endless-go, hit that gap twice on the E-2189 land, and recorded ERR-0020 (error 1540, surface tmux:status-line).

Decided (Q1 A, Q2 C, Q3 A):

1. Make `just go` atomic for every caller. Build to bin/endless-go.next, then `mv -f` it over bin/endless-go. A rename within one directory is atomic, so no reader ever sees a missing, half-written or mixed binary. A failed build leaves the old binary in place and no stray .next file behind.

2. In land, build before migrating and swap right after. Step 5.6's post-migration `_rebuild_main_binary` goes away. Instead:
   - after the ff-merge and before `_apply_branch_schema_changes`, build main's endless-go to the temp path (the same build `just go` runs, stopping short of the rename);
   - immediately after the migration commits, rename it into place;
   - a build failure now stops the land BEFORE the database changes. The message says the code is merged but neither the migration nor the landing record has happened, and that re-running `just land` finishes both (the ff-merge is a no-op on re-run).
   This needs a build-only/swap-only split. Either `just go` takes a mode, or two recipes (`go-build-next`, `go-swap`) with `go` running both. Pick whichever keeps the build flags in one place.

3. Self-dev only, as today. A downstream project's installed binary migrates on connect itself, so it has no gap.

Tests:
- Go/Python unit: land calls the build before `_apply_branch_schema_changes` and the swap after it, and never builds after migrating.
- Build failure before migration: the database version is unchanged and the message names the re-run.
- `just go`: the result is atomic. No bin/endless-go.next is left on success or failure, and the old binary survives a failed build.
- Verify suite: an isolated self-dev land that adds a migration while a loop polls the binary's status line, asserting no ERR-0020 is recorded.



Grown scope (asked and decided mid-implementation, "Land clears its own"):

4. Building first and swapping by rename shrinks the window from a compile to tens of milliseconds, but not to zero: a status-line process that starts on the old binary just before the migration commits reads the new version and records ERR-0020. Measured at a poll every ~160 ms, 2 of 10 fixed lands still recorded one. So after the landing is recorded, the land clears exactly that incident: the fingerprint naming both versions (from `endless-migrate up`'s from/to, built Go-side by monitor.DatabaseAheadFingerprint so it cannot drift from what RecordSchemaRefusal records), first seen at or after the moment the land began migrating. It goes through a new hidden `endless-go errors clear-land-schema` verb (faults.ClearFingerprintSince), with a matching hidden Python passthrough for CLI parity. It never fails the land, and it leaves alone an incident opened before the window.

5. The swap is an in-process os.replace rather than spawning `just go-swap` (~28 ms saved, all of it exposure). `just go-swap` stays as the second half of `just go`.

6. ERR-0020's remedy text (codes.go, docs/errors.md) now says a self-dev land clears the one it causes.
