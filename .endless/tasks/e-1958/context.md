Sandbox provisioning is gated on `self_dev: true`, so only Endless itself gets per-worktree sandboxes: `_maybe_auto_sandbox_bind` (worktree_cmd.py) returns early for other projects and `config.py` resolves the sandbox root to None for them.

That gate was never an intended design decision — the intent was that EVERY project gets a sandbox, because a sandbox is where a worktree stores the configuration a task needs in order to be verified, and essentially no real software project has zero configuration files.
