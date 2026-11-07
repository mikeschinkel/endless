Mike's answers: Q1, how developers work against sibling code once replaces are gone, is resolved by E-2561's go.work command (and E-2563 for task worktrees). Q2 = B: a Go test fails when go.mod has a replace, instead of a CI grep or a hook.

go-work-init reads go.mod's replace lines (justfile, recipe go-work-init), so it becomes a no-op once they are removed; removing it rather than rewriting it avoids duplicating E-2561 and E-2563.

A trial in a temp copy with the replaces removed built and vetted clean against go-cfgstore v0.4.0 and go-cliutil v0.5.0; the final versions are whatever E-2209's chain publishes.
