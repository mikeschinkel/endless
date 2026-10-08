`worktree land` Step 3 partitions main's `git status` into user files and
Endless-managed files, then runs `git add` and `git commit` on the managed ones.
Endless's background recorder commits those same paths on main ("Endless: record
ledger entry") on its own schedule. When it commits between the status read and
the land's commit, git has nothing left to commit, prints that on stdout and
exits 1.

The land retries only on index-lock contention, so this case raises the
"git could not auto-commit main's endless-managed files" refusal with
`auto-commit failed:` and an empty git message — an instruction to "act on what
git said below" when git said nothing on stderr. Nothing is merged, and a re-run
normally succeeds.

Seen on 2026-10-08 landing E-2158: the land failed this way with main's tip a
fresh ledger-entry commit, and both checkouts clean afterwards.
