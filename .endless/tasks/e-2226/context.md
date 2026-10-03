Hit landing E-2156 on 2026-10-03. `just land` refused: "main gained 3 migrations since this branch forked, and the branch adds 3 migrations of its own", naming 00011_auto_spawn.go, 00012_supersedes_relation.sql and migrations_test.go on BOTH sides, and told the agent to renumber the branch's copies to 00013/00014.

The branch added no migrations. Main's history had been rewritten after task/2156 forked, so the commits carrying those migrations existed twice under different SHAs (E-2189's commit was 3fe973d99 on main, b15af387a on the branch). `git diff main HEAD -- internal/schema/migrations` was empty. `git rebase main` dropped the duplicates as previously applied, and the land gate was then satisfied.

The danger is the advice, not the refusal: an agent that follows the printed steps renumbers identical migrations and lands duplicates — the exact corruption the gate exists to prevent.
