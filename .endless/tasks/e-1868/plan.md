## Evidence from E-2144 (2026-09-15): one concept, two vocabularies

E-2144 gave tasks a `superseded` status, so the two record kinds now agree on
the STATUS but not on the RELATION that names the successor:

| | status | relation | command |
|---|---|---|---|
| decision | `superseded` | `supersedes` / `superseded_by` | `decision supersede <id> --by <new>` |
| task | `superseded` | `replaces` / `replaced_by` | `task replace <old> --by <new>` |

Same concept, two words. Mike, 2026-09-15: "Since we are merging decisions back
into tasks, we'll have to resolve eventually, if not now."

E-2144 did NOT resolve it, deliberately: `dep_type` is a stored value, so
renaming `replaces`/`replaced_by` is a data change with a migration, and the
merge is what decides which vocabulary survives. Recording it here so the
merge's plan accounts for it rather than discovering it late.

Note also that `decision supersede` has always required `--by`, and E-2144 gave
the task side the same rule (`_require_a_replacement_for_superseded`): the
status names a successor, so it is refused without one. That rule should carry
across the merge intact — it is the reason the two statuses mean the same thing.
