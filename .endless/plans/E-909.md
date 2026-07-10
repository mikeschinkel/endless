You are a worktree-bound Claude Code session spawned by the now-ready-auto orchestrator to implement E-909 end-to-end.

# Your task — E-909

Read the task description: `endless task show E-909`. The "what" lives there, not in this prompt.

# Your environment

- **Worktree**: `/Users/mikeschinkel/Projects/endless/.endless/worktrees/e-909` — you started here. Run `pwd` to confirm.
- **Branch**: `task/909-task-deps-llm` — already checked out.
- **Hook override**: this worktree's `.claude/settings.json` points endless-hook at `<worktree>/bin/endless-hook` (already built).
- **Main checkout**: `/Users/mikeschinkel/Projects/endless` — DO NOT edit there.

# Procedure

1. `endless task start E-909`.
2. Find the precedent: search `src/endless/cli.py` for an existing `--llm` click option on a sibling command (list/show/next/active/recent/search). Read both the click-option declaration and the function body to understand the LLM output branch.
3. Locate the `task deps` click command in `src/endless/cli.py` (search for `@task_cmd.command("deps")` or similar). Read its current handler — the LLM branch you need to add must mirror what the precedent commands do (compact one-line-per-task format, no decorations).
4. Implement: add the `--llm` option and the LLM-format branch to `task deps`. Match the precedent's option help string style, output style, and field set.
5. Build: `just build` (do NOT run `just install` from this worktree — per repo CLAUDE.md).
6. Manual test from the worktree (use `PYTHONPATH=src endless ...` to run this worktree's code, NOT the globally installed CLI):
   - `PYTHONPATH=src endless task deps E-909` — confirm the human format still works.
   - `PYTHONPATH=src endless task deps E-909 --llm` — confirm compact format. Pick a task with actual deps if E-909 has none; `E-1144`, `E-1135`, or any recently-merged task should have parent/child relations to verify against.
7. Commit on `task/909-task-deps-llm` with a message in the repo style (see `git log --oneline -10`). Co-author footer per CLAUDE.md.
8. **Outcome (REQUIRED before flipping status)**: `endless task update E-909 --outcome "<one paragraph>"`. Cover: final commit ref, what diverged from this prompt's assumptions if anything, what surprises you filed as follow-up tasks. This is the single most valuable artifact of your session — the gap between planned and shipped.
9. `endless task update E-909 --status verify`.
10. Final message to Mike: 1-2 sentence summary plus the tmux switch line below.

# Hard rules

- Stay focused on E-909. New issues become new tasks (`endless task add "..." --description "..." --parent E-909`), never sneak fixes in.
- Do NOT merge to main. Orchestrator handles landing.
- Do NOT run `just install` from this worktree.
- If you create a `.endless/plans/E-909.md` file (or any other plan file), commit it as part of your task commit. Untracked files block worktree cleanup.
- If anything is ambiguous or forces a design choice not covered above — STOP and tell Mike (with the tmux switch line). Do not guess.
- Do NOT mark E-909 `confirmed` or `assumed`. Only `verify`.

# Progenitor — how Mike returns to the orchestrator

You were spawned by the orchestrator session in tmux window `active:endless-now-ready-auto` (window 7 of session `active`). Make this prominent in your final message:

```
tmux select-window -t active:endless-now-ready-auto
```
