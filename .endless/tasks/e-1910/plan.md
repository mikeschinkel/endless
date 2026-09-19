`./tests/tasks/e-1906-verify.sh` reports 44 passed, 2 failed on main:

    ✗ no live `needs_recap` anywhere in the source tree
        got: tests/tasks/e-1905-verify.sh:375:    needs_recap INTEGER NOT NULL DEFAULT 0,
    ✗ no live `summary_seq` anywhere in the source tree

Confirmed pre-existing and NOT caused by E-1901: reproduces identically from the
main checkout, same two checks, same counts.

Cause: E-1905 and E-1906 landed in close succession. E-1906's section 1 is a
grep-based "zero hits in the source tree" assertion for the identifiers it
removed. E-1905's verify script legitimately contains a historical `CREATE TABLE
sessions` fixture that still lists `needs_recap` and `summary_seq` — it is
asserting against a pre-drop schema on purpose. E-1906's grep cannot tell a live
reference from a deliberate historical fixture, so the sibling script trips it.

Fix direction: narrow E-1906's grep to exclude sibling task-verify fixtures (it
already excludes `internal/schema/changes/`, which has the same
period-accurate-schema property), rather than editing E-1905's fixture — the
fixture is correct and load-bearing for E-1905's own assertions.

Both symptoms are one defect and are filed together deliberately: they share a
single cause and a single fix, and splitting them would put two sessions in the
same grep block.

This is also a concrete instance of a coordination hazard worth noting: two
tasks whose changes overlap the same files landed in parallel and broke each
other's verification, with neither task's own suite green afterwards.
