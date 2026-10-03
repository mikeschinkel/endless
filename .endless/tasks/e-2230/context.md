Raised during the E-2223 brainstorm. Mike tells whether a task is finished by whether its worktree is settled, and `session status` / `session monitor` show a worktree as "unsettled" when it isn't. That signal doesn't fit findings-lane tasks (research, brainstorm), whose deliverable is outcome text. Brainstorm and research tasks no longer leave content mirrors (plans, outcomes) in their worktree, so their worktree and sandbox may serve no purpose. They get created anyway, because every claim creates them.

`unreviewed` already means "the agent finished; the user needs to read the outcome", but the monitor doesn't lead with it. So a user has no clear cue for when to ask the agent to finish, or to read and complete the task.

Open questions:
- Do findings-lane tasks need a worktree and a sandbox at all?
- How does the monitor show agent-finished vs. not finished for these tasks, separately from the explicit `completed`?
- Is a spike (throwaway code built to answer a question, never landed) a new task type, or a flag on research, and where would that be set? Its distinguishing behavior is that `worktree land` refuses and the outcome carries the answer.
