Guides, docs, MEMORY, and spawn-prompt templates tell agents to author scratch content at a /tmp path, then load it.

/tmp is system-global and ephemeral: content written there and only referenced (not loaded) from the ledger, or left when a worktree is dropped, is lost — and /tmp is wiped on reboot.

This is the suspenders half of belt-and-suspenders (with the multiline-mirror task): a forgotten-but-not-yet-loaded scratch file stays co-located and recoverable instead of vaporized.
