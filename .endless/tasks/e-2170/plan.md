The residual gap ED-1596 accepts. Dropping per-task hook delegation removes the
only pre-land way to exercise candidate hook code against a live Claude session.
Verify suites cover what a hook OUTPUTS — its JSON, its database writes. Nothing
covers whether Claude Code HONORS it: that a PreToolUse block really stops a
tool call, that `additionalContext` really reaches the model, that a Stop gate
really holds a turn.

Today that is discovered the first time a shipped feature is used.

# The experiment to run first, because it forks the whole design

`hook.go`'s header records that internal headless `claude -p` calls set
`ENDLESS_NO_HOOKS=true` "to suppress the hook" — so a headless `claude -p`
FIRES hooks normally. The verify runner already builds a temp `HOME` and
`XDG_CONFIG_HOME`.

So the cheap harness may already be assemblable: a temp home whose settings
point at the candidate binary, `claude -p "<prompt>"`, then assert on what the
hook wrote to that home's throwaway database. If that works, no containerization
is needed and this becomes a small addition to the existing harness.

Run that before designing anything. It is the difference between a fixture and
an infrastructure project.

# What it may not reach, which is the real question

`claude -p` is non-interactive. The behaviors that motivated this are the
interactive ones:

- does a PreToolUse non-zero exit actually stop a tool call
- does a Stop gate actually hold a turn, and does the bounce loop terminate
- does `additionalContext` actually arrive in the model's context

If `-p` exercises none of these, the options get more expensive — driving a
pty, containerizing a full session, or accepting that this class stays untested
pre-land and is covered by a fast rollback instead.

# Scope notes

- Deliverable is a decision about approach, not an implementation.
- Containerization is a candidate, not a premise. It was raised as likely; test
  the cheap path first so the cost is chosen rather than assumed.
- Whatever is decided has to run under the `.endless/tasks/` runner's isolation
  contract, or it inherits the failure mode that harness exists to prevent: a
  suite that reaches the real ledger.
