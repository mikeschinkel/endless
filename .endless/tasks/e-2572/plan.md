# Research: which tmux server each endless command may run on

## What this task decides

E-2519 made goto and resume route to the task-session server. That raised a
policy question it could not settle inline. This task answers four questions:

1. **Refusal.** Should an endless command refuse to run outside an `endless-*`
   tmux server? If so, which commands?
2. **Assignment.** Which commands belong to the console server, which to the
   task-session server, and which to neither?
3. **Scratch server.** Should an `endless-scratch` server exist for the commands
   that belong to neither?
4. **Claude hooks.** May a Claude hook bind a pane on a server other than
   `endless-tasks`? What happens to a Claude running on the console, on the
   user's own tmux server, or outside tmux?

The answers must form one policy that holds at every entry point: `endless
start`, `project register`, the verify runner, background jobs, the test suites,
and Claude hooks outside tmux.

## Standing of earlier input

The questions are open. These are inputs to weigh, not settled answers:

- ED-1607 (Mike: obsolete) and ED-1673 (proposed).
- The answers recorded in the previous version of this plan.
- The planning survey in this task's analysis, which is a starting point.

Where the evidence disagrees with an input, the outcome says so.

## Method

1. Separate two facts the questions tend to merge: where a command is **typed**
   and which server it **acts on**.
2. Sort commands into classes by what they need from tmux. Use the analysis
   survey, and read code only where a class is unclear. Individual call sites
   matter only when one changes a class or an answer.
3. For each question, set out the realistic options, what each costs, and a
   recommendation.
4. Test the proposed policy against each entry point above. Run a command only
   where reading the code leaves the behavior uncertain. Any run stays in the
   worktree sandbox with a private socket directory.
5. PRODUCT: say how each answer behaves for someone else on another machine,
   including a person who uses tmux for their own work and a person who does
   not use tmux at all.
6. Ask Mike about any choice that is his to make before writing it as the
   policy.

## Deliverable

`outcome` holds:

- A recommended answer to each of the four questions, with its reasons and the
  options rejected.
- One table: command class, where it may be typed, which server it acts on, and
  what it does when that server is not running.
- The entry-point check.
- Proposed wording for ED-1673, for Mike to accept or change.
- Where today's code departs from the policy, summarized by class.

## Out of scope

- An exhaustive inventory of tmux call sites.
- Designing or implementing the fixes.
- Filing tasks as part of the deliverable. The outcome lists suggested
  follow-ups, and Mike decides which to file.

## Done when

Mike can read the outcome and accept or change each of the four answers without
needing to open the code.
