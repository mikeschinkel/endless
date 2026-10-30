Found during gomion E-2454 (2026-10-28), working from that task's worktree, gomion's .endless/worktrees/e-2454, with endless 0.1.0.

`endless task update` run with the worktree as the current directory sometimes applies part of the update, prints success lines for it, and then fails with:

    Error: [Endless] task update: Not in a registered project directory. Specify a name: endless task <command> --project <name> ...

Observed three times:

1. `endless task update E-2454 --items-file items.toml --plan-file plan.md` printed "✓ Wrote plan items", "Plan items of E-2454: 14 changes", "✓ Wrote plan to .../plan.md", then the error. The items reached the database and the plan.md mirror was written and committed on main, but the database plan stayed the old placeholder. The agent read the success lines as success and asked the user to approve a plan the user could not see; `task show --plan` still showed the placeholder.
2. `endless task update E-2454 --plan-file plan.md` alone: "✓ Wrote plan to .../plan.md", then the same error, and again the database plan was unchanged. In the same session `--items-file` alone from the worktree succeeded.
3. `endless task update E-2454 --status unverified` failed with the same error, without a success line.

Each command succeeded unchanged when rerun from the main checkout. Other task commands from the same worktree (task show, task plan add, task reconcile, task verify, worktree check) resolved the project fine, so the worktree is recognised as gomion's in most paths and not in this one.

Two problems: task update does not resolve the project from a task worktree the way other commands do, and when it fails it has already written the mirror and part of the update, while the success lines it printed say the whole thing was done.
