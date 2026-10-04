Local main normally runs hundreds of commits ahead of origin/main, because land never pushes. When origin gains even one commit main lacks (a README edit made on GitHub), integrating it is expensive: on 2026-10-03 a `git pull` under `pull.rebase = true` replayed 232 unpushed commits with new SHAs, which broke every open task branch's land (see the linked land-gate task) and, earlier, the ledger auto-amend (E-1955 recorded the same rewrite).

Endless never runs `git pull` today, so no Endless command depends on the user's pull config. A job that syncs must keep it that way: bare `git pull` behaves differently under each user's `pull.rebase` / `pull.ff`, and Endless does not own anyone's git config.

Known consequence of pushing often: the ledger auto-amend in internal/events/commit.go only amends a tip no other ref contains, so a pushed tip is appended to instead — more small "record ledger entry" commits, not breakage.
