# Vision

*What we envision Endless will be.*

Endless started small. One developer, a growing pile of software projects, and a
new kind of collaborator, Claude Code, that could actually do the work if you kept
it pointed in the right direction. The hard part was never getting a single
session to write code. The hard part was keeping track of a dozen of them at once:
which one was researching, which was mid-refactor, which had finished and was
waiting on you, and what each was even supposed to be doing. Endless began as the
answer to that, a way to manage many concurrent AI sessions, first across terminal
tabs, then across tmux windows, without losing the thread.

That origin still describes what Endless does today. But it is not what we envision
Endless will be.

## The arc

We think the interesting future is not one developer steering many sessions on
their own projects. It is *many* developers, each steering many AI-agent sessions,
collaborating on the same shared projects, with Endless as the substrate
underneath that makes it hold together.

The shift is one of role. A developer's job stops being *doing the work* and
becomes *steering the work*: directing research, coding, bug-fixing, and
brainstorming across a fleet of sessions rather than typing every change by hand.
We think that shift lets a person hold something like two orders of magnitude more
concurrent work in flight than they could before. That is only livable if the
tooling absorbs the coordination cost instead of handing it back to the human.
Everything below is in service of that.

## Steering, not typing

The unit of work is a task, and the loop runs through it. A developer has Claude
Code file the task or tasks, works with it to shape a plan, and then spawns each
task into its own session: a fresh window, a task-specific git worktree, and an
isolated config-and-database sandbox so nothing collides with anything else. The
session works the plan on its own. When it is done, it hands back a verification
script, a concrete and runnable way to confirm the change does what it claimed.
The developer runs it, gains confidence, and lands the work, merging the worktree
back into `main`.

The point of that loop is that the human stays at the altitude of intent. They
decide *what* should happen and *whether it happened*, and delegate the rest. We
envision that loop getting richer over time: tasks that start unattended in the
background and get promoted to the foreground when they need attention, plans that
carry more of their own context, verification that a second agent can
adversarially challenge. But the shape stays the same. The person steers, the
agents drive.

## Lowering the human's review burden

If there is one thing we want Endless to be known for, it is this: **it should
cost a human as little attention as possible to stay in control.**

Steering a hundred sessions is worthless if each one demands the same scrutiny a
single session would. The bottleneck stops being the work and starts being the
review. So we treat the human's attention as the scarcest resource in the system
and design against spending it.

That has consequences everywhere. Agents report back in structured, skimmable
form, the shape of what changed, what was verified, what is still open, rather than
in walls of prose a person has to reconstruct meaning from. The tool computes what
it can compute and does not make the human re-derive it. A session hands over a way
to *check* its work instead of asking to be *trusted*. When nothing needs a
decision, it says nothing; when something does, it surfaces exactly that. The
ambition is a system where a developer can hold an enormous amount of work in
flight and still, at a glance, know what is true, because the tooling did the
tedious part of knowing for them.

This is not a feature. It is the philosophy the features answer to.

## Collaboration through git, not a server

For many developers to share a project, most tools reach for a central server, a
service that owns the truth and that everyone has to be online to talk to. We do
not want that. We envision developers collaborating the way they already
collaborate on code: through ordinary git. You push, you pull, and your local view
of every task, plan, and decision rebuilds itself from the same committed history
everyone else has. There is no central authority to run, to trust, or to be down.
Two people can work the same project at the same time and never collide.

## The ledger underneath

None of the above works without a foundation you can trust, so at the bottom of
Endless is an append-only ledger: a JSONL write-ahead log that is the actual source
of record. The database everyone queries is a projection of that log, rebuildable
at any time, never the authority itself. History is written once and never
rewritten; when the shape of the data needs to change, we replay the log into the
new shape rather than editing the past. That is deliberately invisible plumbing.
But it is what lets many developers share one project over plain git, lets each
task run isolated from the rest, and lets the whole system be rebuilt from first
principles whenever it needs to be. The reliability of everything above rests on
it.

## Where this goes

We are honest that much of this is still ahead of us. Endless today does the
single-developer part well and is reaching toward the rest. We would rather state
the direction plainly and be measured against it than pretend the destination is
already here.

The direction is steady: give a person the ability to direct a great deal of
software work through AI agents, keep the cost of staying in control low enough
that the ability is real, and make the whole thing shareable through the tools
developers already use. That is what we envision Endless will be.
