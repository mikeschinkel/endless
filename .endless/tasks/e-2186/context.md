About 24 non-test files still read or set XDG_CONFIG_HOME although Endless state moved into the worktree sandbox.

The stale claim in internal/verifycmd/verify.go hid that verify lost its fresh DB.
