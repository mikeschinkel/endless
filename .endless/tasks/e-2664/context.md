Mike raised this on 2026-11-19, in a go-cliutil session that had just run `/needs-plan` on a go-cliutil brainstorm. The skill had the calling session do the brainstorm itself: it wrote out 15 questions with options, pros, cons and recommendations.

What Mike sees going wrong today:
- Endless requires every task to have a plan, so a session that files a task in another project also plans it. That fills the filing session's transcript with the other project's material and uses a lot of its context.
- For research and brainstorm tasks, "planning" means doing the task's own work early, in the wrong session.
- Some sessions file many tasks and others file none, so the planning load ends up very uneven.
- Starting work in a project that has no running session needs a Claude session just to file the first task.
- `/needs-plan` is a skill. Mike's goal is that Endless needs none of its own skills: `endless guide` should be enough, so that he can just say "file a plan task to do X."

## Record of the discussion (2026-11-19, go-cliutil session for E-2654)

Mike's own words, quoted:

- On filing: "When I am in a prior session and we decide to file a task, that session could provide Context, Analysis, and possibly Brief, but basically do as little additional work as possible so as to hand-off the actual planning work for the task to a to-be-spawned session. It's possible we could even have you file it and immediately spawn it (without refocusing the tmux window) so that the work to plan it can get started immediately."
- On a brief: Mike's first idea for its wording was "Write a Task Brief for a future agent. Define the objective, required context, and success criteria, but do not perform the research or generate solutions yet."
- On scope in a brief: "not a comprehensive scope w/excluded plan items; that would be what the planning task does." Deliverable, success criteria and constraints are written by the filer only "if the user specified them already".
- On why fixing `/needs-plan` alone is not enough: "that will be necessary, but is not sufficient because of the cross-project problem, and the fact some sessions file a large number of tasks and I have been using session monitors to keep track of tasks so I end up with some sessions needing to plan many tasks and other session plan no tasks."
- On a type rather than a status: "a status does not have a handoff document, but a task type does, and a plan task would need a very different handoff document than a todo." Also: "we'd need a way to inject a todo handoff once the planning is done, but I have some ideas about that." The spawned session should ask Mike for those ideas.
- On the spawn gate: having a `plan` type means no `--to-plan` flag is needed, "because spawn could gate based on type; it can't otherwise gate on status, unless we added yet another status type".
- On bootstrapping: "I could easily file a task with minimal info from the CLI alone, and then spawn it to start planning it w/o having to have a Claude session to do the initial task filing for me."
- On what a filer must give: "Only NEED [a brief], but Context and Analysis should be ABLE to be provided in the case the filing session has that information to provide."
- On skills: moving the `/needs-plan` rules into the plan handoff "is a HUGE plus, because it eliminates a required skill and moves it into the CLI; my goal would be that Endless CLI does not require any of its own skills for the user to be productive." And: "Ideally 'endless guide' would be enough to allow you to know how to file a plan task and then I would not need a skill at all, I could just say 'file a plan task to do X.'"
- On the eventual type: "Filer proposes (or not), agent proposes, ratifies or proposes an alternate type, but only for 'do' tasks; todo, bugfix, docs (and planned future type), etc. and not for outcome tasks like research and brainstorm." The `docs` type does not exist yet; `task add --type` currently accepts todo, bugfix, research, epic and brainstorm.
- On research and brainstorm: "Thus far I think I have always know[n] it is a research or brainstorm task in advance. Also, you don't 'plan' those, you give them a brief (or we soon will make that change, if that is not how it already works.)" I proposed that a `plan` session turn a research or brainstorm task into a brief; Mike rejected that as "too much process theatre".
- On spawning at filing: Mike proposed a `--no-spawn` flag, so spawning is the default.

The `/needs-plan` command whose rules move into the plan handoff is a user-level Claude Code command named `needs-plan.md` in Mike's Claude commands directory. Its full text, as most recently invoked, is the rule set to carry over: present every open question with every option; for each option give only the pros and cons specific to it; write "none" where there are none; list what does not count as a con; ask only questions with real tradeoffs; list decisions with no tradeoff under "Decided without asking"; use only defined terms, and treat as settled only an accepted decision, a glossary term or the user's own words; end each question with a recommendation; and itemize the final plan with plan items.
