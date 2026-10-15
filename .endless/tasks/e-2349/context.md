go-cfgstore was registered in April, before `endless project init` existed, as were almost all other registered projects. Bringing it up to date in E-2206 turned up these gaps:

- The `.gitignore` had none of the entries Endless needs. `project init` adds them, but only if someone knows to run it.
- `.endless/config.json` existed but was never committed. Nothing in Endless commits it.
- There was no CLAUDE.md or AGENTS.md pointing an agent at `endless guide`, and nothing in Endless writes one.
- The config had no `"minimizer": {"enabled": false, "optimizer": false}` block. Without it, every reply in the project is blocked by the `task report` gate, and the session cannot proceed (the minimizer is a feature being retired).
- Running `endless project init --infer` inside a task worktree registered the worktree itself as a new project named `e-2206` and overwrote its config. Related to the second-checkout refusal in E-2217.
- The repo builds with a Makefile. Mike's current policy is to port Makefiles to justfiles wherever they're found.

PRODUCT: every existing user with projects registered before `init` existed has these gaps and no single command that fixes them. `endless doctor` (E-2287) checks and reports; this task repairs.
- Clearing the mistaken `e-2206` registration took `project unregister`, which wrote `"status": "unregistered"` into the worktree's `config.json` (the project's real config, about to be committed) and added the worktree path to the ignore list. Endless has no command that removes a mistaken registration cleanly.
- go-cfgstore has no `.endless/tasks/_harness.sh` or `_guard.sh`, so a task here cannot write the standard harness-based `verify.sh`. E-2206's suite had to be a `verify.toml` with a self-contained `sh` check.
