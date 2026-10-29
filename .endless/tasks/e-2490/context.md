A task's code commits name their task only by convention: agents start the subject with `E-NNNN:`, and nothing checks or relies on it. A Git trailer (a `Key: value` line in a commit message's last paragraph, which Git and hosts read as structured data) would make the link data: `git log --format='%(trailers:key=Endless-Task,valueonly)'` maps commits to tasks without parsing subjects, and it survives a rebase, much as Gerrit's Change-Id does. Mike asked for this on 2026-10-29 (in E-2375's session), wanting the CLI rather than each agent to be responsible for adding it.

Mechanisms raised, not yet chosen:
- a `commit-msg` Git hook Endless installs per repo, which reads the task from the branch name (task/NNNN) and appends the trailer; it applies to every commit made by any tool, including plain `git commit` and GoLand;
- an `endless git` command passing through to git and adding the trailer on commit; it applies only when the agent or person uses it;
- Endless's existing Claude Bash hooks rewriting or refusing a bare `git commit` from an agent session.

Whether the `E-NNNN:` subject prefix stays is part of the plan: CI run titles show the subject, so dropping the prefix would drop the task ID from them. Key spelling should follow whatever E-2488's trailer question settles (Mike leans to Dashed-Proper-Case, `Endless-Task`).
