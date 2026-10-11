When `endless worktree land` (via `just land`) hits a rebase conflict while
rebasing the task branch onto main, it reports:

    A source file conflicts. The rebase has been aborted and your branch is
    exactly as it was, but the conflict state was recorded first

and tells the agent to run `endless worktree diagnose <task>` first.

Observed 2026-10-11 landing E-1562: the rebase had NOT been aborted. The
worktree was left mid-rebase (`interactive rebase in progress`, HEAD detached at
main's tip, one file in conflict). So the message was false, and the step it
prescribes could not run: `worktree diagnose` refused with "Worktree ... has no
branch (detached HEAD), so there is nothing to diagnose."

Likely cause, in `land_worktree`'s rebase-failure branch: after building the
refusal it runs `_git_run(["rebase", "--abort"], cwd=worktree_path,
check=False)` and raises the refusal whatever the abort did. The abort's result
is never checked, and the refusal text asserts the abort unconditionally. A
`verify` run was going in the same worktree at the time, so the abort plausibly
lost the race for `index.lock` — the same contention E-2174 retries for the
rebase itself but not for the abort.

Why it matters: an agent told "your branch is exactly as it was" will act on a
worktree that is actually mid-rebase with HEAD detached — committing onto a
detached HEAD, or running a fresh rebase on top — and the recovery tool it is
pointed at refuses outright.
