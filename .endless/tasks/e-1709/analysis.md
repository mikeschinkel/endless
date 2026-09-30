Fix: build the worktree binary up-front, before the apply-change/record steps (or make the E-1664 guard reject a stale binary, not just a missing one).

No chicken-and-egg — the up-front build needs nothing the land doesn't already have.
