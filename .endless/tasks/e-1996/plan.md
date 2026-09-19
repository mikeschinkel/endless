# Seed / framing

E-1995 (automated dispute resolution) assumes a live filing session to route an
objection to. When that session is gone, or when a primed session must be woken
to answer, the natural move is to let one session **resume** another. That is a
meaningful escalation in agent autonomy and should be configurable to require
user approval before it happens.

Requiring approval creates a UI problem Endless does not currently have a way to
solve: **how do you get the user's attention when they are somewhere else?**
Sessions live in tmux windows scattered across panes, and the user may be in a
different window, a different project, or away entirely. An approval prompt that
appears only in the requesting session's pane will be missed.

## What to explore

- What "attention" means across many tmux windows: a status-line badge, a
  window-title marker, a bell, a dedicated approvals pane, an OS notification,
  or something else. Which of these survive the user being away for an hour.
- Whether this is one queue for the whole machine or per-project.
- What is actually being approved: one session resuming another, an escalated
  open question needing an answer, a task parked on questions, a dispute that
  hit its round cap. These may be one queue or several.
- Whether it integrates with `project monitor` (E-1976, cross-session attention
  triage) or stands beside it. E-1976 already targets "cross-session attention"
  — this may be the same surface, or the interactive half of it.
- Approve-once vs. standing grants: "this session may resume peers in this
  project" is a different grant from "approve this one resume."
- What happens when the user never responds. A blocked request must degrade to
  something visible rather than a silent hang.
- Whether the approving surface must be a TUI at all, given `endless` is a CLI.

## Constraints

Judge as a product, not as this machine (PRODUCT): the design cannot assume tmux
is present, cannot assume the user runs worktrees, and must degrade sensibly for
someone on a fresh install with a single terminal. Say explicitly what it does in
`self_dev` mode versus for a project that merely uses Endless as a tool.
