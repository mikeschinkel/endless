E-2002 stops the Go hook auto-registering a second project for a directory that already has one, and makes lookups tolerate rows stored unresolved — but it deliberately ships no migration, so a ledger that already caught the bug keeps its junk row (typically named <project>-2, path unresolved, zero tasks, bound to whatever sessions ran while the bug was live).

It is inert after E-2002 (lookups prefer the lower id) but still shows up in `endless project list` and in any per-project rollup.
