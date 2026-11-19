Mike raised this on 2026-11-19, in a go-cliutil session that had just run `/needs-plan` on a go-cliutil brainstorm. The skill had the calling session do the brainstorm itself: it wrote out 15 questions with options, pros, cons and recommendations.

What Mike sees going wrong today:
- Endless requires every task to have a plan, so a session that files a task in another project also plans it. That fills the filing session's transcript with the other project's material and uses a lot of its context.
- For research and brainstorm tasks, "planning" means doing the task's own work early, in the wrong session.
- Some sessions file many tasks and others file none, so the planning load ends up very uneven.
- Starting work in a project that has no running session needs a Claude session just to file the first task.
- `/needs-plan` is a skill. Mike's goal is that Endless needs none of its own skills: `endless guide` should be enough, so that he can just say "file a plan task to do X."
