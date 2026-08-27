# The ask

Find a way to generate a hand-off document for a currently running session.

When I run `endless task spawn` from the CLI it generates a handoff, opens a
tmux window, runs Claude in a pane of that window, and passes it the generated
handoff. But there is no command I know of that I can tell an already-running
Claude session to run to get the handoff, if it did not get one at spawn.

Determine whether a handoff command in fact exists. If it does not, we need a
task to create one — or some other solution you recommend.
