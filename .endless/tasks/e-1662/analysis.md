Current auto-provision (_maybe_auto_sandbox_bind, worktree_cmd.py) already invokes 'endless-go sandbox init/bind' -- PRODUCT-compliant, NOT just; extend that pattern. Gap: it never builds the worktree binaries (no bin/) nor installs the hook override, so hook + CLI run the global binary. PRODUCT: invoke a project-declared provision script, never 'just' from shipped code. Coordinate with E-1368 (replaces bin-sandbox wrappers with binary self-detect) so this doesn't entrench bin-sandbox. Touch-points: _maybe_auto_sandbox_bind, the 'endless-go sandbox' subcommands, .endless/config.json (declare provision script), task_cmd.py claim/spawn.

## From the description

Touch-points in analysis.
