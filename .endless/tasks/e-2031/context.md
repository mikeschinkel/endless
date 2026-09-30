tests/tasks/e-1573-verify.sh fails 4 of 42 checks on main, unrelated to E-2029 (verified identical on main and in the E-2029 worktree).

One root cause: the suite pins exact prose from docs/guide/orchestration.md, and that prose has been rewritten since without the suite being updated.

The four: (1) 'unknown type falls back to the `task` variant' — sentence reworded; (2) 'All `needs_plan`' and (3) 'All `in_progress`' — assert the pre-rename status vocabulary, now unplanned/underway; (4) 'spawn section is id-free (shipped-doc rule)' — E-1851, E-1953 and E-698 have since been written into the shipped guide, which the rule forbids.
