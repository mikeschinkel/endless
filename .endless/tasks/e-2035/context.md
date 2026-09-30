`--db sandbox` isolates the DB and, since E-1729, the event ledger — but nothing else.

Hit on E-1920: a sandbox-routed `decision add` wrote decision mirrors into the worktree and auto-committed two junk commits onto the task branch.

E-1729 already fixed this exact mechanism for the ledger and established the shape (sandbox-local dir under ConfigDir, no git commit, since a sandbox is disposable and not a repo), but it was applied at one call site instead of made a rule, so E-1747's doc mirrors re-broke it.
