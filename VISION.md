# Vision for Endless

*Manage 50+ AI tasks without becoming overwhelmed.*

Endless started small. One person, a growing pile of software projects, and a new
kind of collaborator, Claude Code, that could actually do the work for you if you
managed to keep it focused on what you think is important while also reining in its
worst impulses.

But the hard part was never getting a single session to write code. The hard part
was keeping track of all of them, all at once: which one was researching, which was
mid-refactor, which had finished and was waiting on you, and what each was even
supposed to be doing.

Endless began as the answer to that: a way to manage many concurrent AI sessions,
first across terminal tabs, then across tmux windows, without losing the thread.

That origin still describes what Endless does today. But we envision Endless becoming
much more, and that vision is expansive, so before laying it out, one reassurance:

## Start Where You Are

**None** of this has to be adopted all at once.

Install Endless and keep using Claude Code exactly as you do today; nothing about
your workflow has to change on the first day.
Then, as you grow comfortable, lean on more of what Endless offers: let it file and
track your tasks, spawn them into isolated worktrees, sandbox their state, and hand
back verification you can check with a single command. The on-ramp is a single step,
and the value compounds as you take the next ones.

## For Each Person, a Myriad of Concurrent Tasks

Endless' goal is for a single person to be able to direct tens, and maybe even
hundreds, of concurrent Claude Code sessions, each working on its own bespoke task.

Spinning up many agents is, by itself, table stakes now; so is having more than one
person on a project. Instead, Endless aspires to make a single person >10x more
productive, measured by how many independent tasks they can carry forward at the same
time without becoming so overwhelmed they are no longer able to manage them all.

Users should be able to scale their in-progress work smoothly from one Claude Code
session to a fleet of them, without the workflow needing to change except for how
many tasks are in flight.

The shift is one of role. A person's job stops being *development* and instead
becomes *product management* and *project management*. They steer the work:
directing research, coding, bug-fixing, and brainstorming across a fleet of
sessions. This is only possible if the tooling absorbs the coordination cost instead
of handing it back to the human. Endless' reason for being is to provide that
coordination.

## The Fundamental Unit: the Task

The unit of work is a task. An Endless user has Claude Code file a task or tasks,
works with Claude to shape a plan, and then spawns each task into its own Claude
session: a fresh tmux window, a task-specific Git worktree, and an isolated
config-and-DB sandbox so nothing collides with anything else. The session works the
plan concurrently with all the others.

Then, when Claude is done working, it hands back a verification script: a concrete
and runnable way to confirm the change does what it claimed. The user runs it, gains
confidence, and lands the work, merging the worktree back into `main`.

The human stays focused on the intent. They decide *what* should be implemented, in what order, and then verify that it was in fact implemented, while delegating the rest.

Every task, even one that starts unattended, runs as a first-class session a person can watch, interrupt, and redirect, rather than a lightweight background agent running without the user being able to chat with it. A task might begin in the background and move to the foreground when it needs attention, but it always stays a real, steerable session.

## Lowering the Human's Review Burden

If there is one thing Endless aspires to provide, it is to minimize the attention required of a human to produce results.

Steering a hundred sessions is worthless if each one demands the same scrutiny a
single session would. The bottleneck becomes the human attention required for
direction and then review. So Endless treats the human's attention as a scarce
resource and designs around the expenditure of that resource.

Agents report back in structured, skimmable form, the shape of what changed, what was verified, what is still open, rather than in walls of prose a person has to reconstruct meaning from. The CLI computes what it can compute and does not make the human re-derive it. Sessions encapsulate verification of a task into a single deliverable unit that can be evaluated by the user with one command or click.

When the user needs to make a decision or take an action, the agent asks or directs;
otherwise Endless urges Claude to stay quiet. Endless seeks to rein in Claude's
penchant for demanding the user spend attention reviewing the summaries it
regurgitates only to demonstrate that it completed its task.

The aim is to let a person juggle an enormous amount of work in flight and still, at
a glance, know the status of every task, because the tooling does the tedious part
for them rather than delegating it to the agent.

This philosophy is a key feature of Endless.

## Sharing a Project

Because the work is organized this way, more than one person can share a project
without much ceremony. Rather than a central server that owns the truth and that
everyone has to be online to reach, Endless leans on the tool already used for code
collaboration: Git. You push, you pull, and your local view of every task, plan, and
decision rebuilds itself from the same committed history everyone else has. There is
no central authority needed to run, and two people can work the same project at once
without colliding.

## The Ledger Underneath

Under all of this is a version-controlled log of activity: an append-only JSONL ledger, a write-ahead log that is the actual source of record. The database everyone queries is a projection of that log, rebuildable at any time, never the authority
itself.

History is written once and never rewritten; when the shape of the data needs to
change, the log is replayed into the new shape rather than the past being edited. It
is deliberately invisible plumbing, but it is what lets each task run isolated from
the rest, lets a project be shared over plain Git, and lets the whole system be
rebuilt whenever it needs to be.

## Where do we go from here?

Much of this is still ahead. Endless today handles single-session and small-fleet
well. What Endless strives for is the rest.

Our direction is immutable though: let a person direct a copious amount of work
through AI agents, minimize the human attention required, and build it using tools
many people already use.

And once all this functionality is in place, Endless believes that it will become
the empowering technology that you will not be able to envision living without.

That is what Endless is meant to become.
