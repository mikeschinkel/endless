Worktree branches lose their base when main's history is rewritten under them — not from amending.

canAmend already refuses to amend a tip another ref can reach, but implements that as SHA reachability, so once `git pull --rebase` rewrites main the guard answers a question the rewrite made meaningless, permits the amend, and the land rebase then conflicts on the append-only ledger segment.
