# Brainstorm: rework `session list`, and add per-session task hiding for `session status` / `session monitor`

Verbatim framing from Mike (2026-08-06):

> I asked for `session list` back when sessions were open-ended and not tied to
> single tasks. So the current list, showing a summary of discussion, is less
> than helpful. Further, different length session state values cause the output
> to be garbled, and icons with a legend — similar to how session status conveys
> information — would probably be more effective. Also, `session list` really
> should show active task id. I think it should also only show the current
> project unless an `--all-projects` flag is used, and thus the Project column
> should be omitted for a single project. A `--project` flag could allow the
> same project-specific output for when not in a current session or not in the
> dir for the project of interest.
>
> More importantly, I need to be able to hide tasks from the current
> `session monitor` and `session status` list, with something to indicate there
> are tasks hidden.
>
> Given this ability to hide, there needs to be a way to unhide without knowing
> the ID. That could be a `--only-hidden` flag on `session status` to provide the
> status for only hidden tasks, and a `--show-hidden` flag to include the hidden
> tasks in the list. You might also propose other ways to allow a user to
> discover which tasks are hidden.
>
> I could see us having `session hide/unhide --task <task_id>` but I could also
> see `task hide/unhide <task_id>`; what do you think?

## The governing motivation (Mike, clarifying — this drives every design choice below)

> I want to hide a task FOR A SPECIFIC SESSION. Consider I have 50 sessions, and
> 3 of the 5 most recently active sessions list the same task in their
> `session status` output. I want to be able to decide that I will deal with
> that task in one session and ignore it in the two others, since I use the
> session list as basically a todo list for keeping the session alive. I archive
> the session once all tasks in its `session status` are resolved. If I have a
> task that multiple sessions are listing then I have to wait to archive any of
> those sessions until that task is done, whereas if I can remove the task from
> 2 of 3 of those sessions, I can archive those two other sessions sooner.

**This is settled, not open for re-litigation in the brainstorm.** Hidden-ness
is a property of the *(session, task)* pair. A global per-task "hidden" flag
would be actively useless here — it would hide the task from the one session
that is supposed to deal with it. The design question is not *whether* it is
per-session; it is how to store, surface, and unwind per-session hiding.

Two clarifications that bound this thread (Mike, 2026-08-06) — both settled:

- **"Archiving" is not a command.** There is no `session archive` and this
  brainstorm must not assume one. Archiving means the human exits the Claude
  session and closes the tmux window. Endless has no hook there and needs none.
- **`session status` lists tasks the session *touched*; touch does not imply
  dependency.** Deciding to archive is a human judgment drawn from many
  signals. Endless's job is not to compute archivability, warn about it, or
  gate it. Hiding is simply the tool that lets the human drop a task out of one
  session's list so that list reflects what they actually intend to handle
  there. **That is the whole feature. Nothing downstream of it is in scope.**

## Scope of the brainstorm

Two related threads, to be designed together because they share the same
"what does this listing show" surface.

### Thread 1 — `session list` is built for the wrong era

Sessions used to be open-ended; they are now 1:1 with a task. The command's
output still reflects the old model.

Problems to resolve:

1. **Discussion summary column is low value.** It was the identifying handle
   when a session had no task. Now the task is the identity.
2. **Ragged columns.** Session state values differ in length and garble the
   table. Candidate fix: fixed-width icon glyphs plus a legend, mirroring how
   `session status` already conveys state.
3. **Missing active task ID.** Should be a first-class column.
4. **Project scoping.** Default to the current project only; add
   `--all-projects` to widen. When output covers a single project, omit the
   Project column entirely (the header line can name it instead).
5. **`--project <name>`** to get the same single-project output from outside
   that project's directory, or when not in a session at all.

Open questions: what the final column set is; whether the legend prints always,
only on a TTY, or behind a flag; whether icons should be reused verbatim from
`session status`'s vocabulary or need their own; how this interacts with
`--json` / machine-readable output; whether `--project` and `--all-projects`
conflict or compose.

**Cross-thread question:** should `session list` gain a hidden-count column, so
a user scanning many sessions can see at a glance which ones have suppressed
work? Weigh purely on whether it earns its column width — this is a display
question, not a safety one.

### Thread 2 — per-session hiding of tasks from `session status` / `session monitor`

Need: suppress specific tasks from a *specific session's* live listing so the
working set stays readable and the session can reach an archivable state,
**with an explicit indicator that N tasks are hidden** (a hidden task must never
silently vanish).

#### Storage model

The natural shape is a join/association carrying the hidden flag: hidden-ness
attaches to the *session↔task association*, not to the task and not to the
session. If that association is already a table, this is one boolean column (or
a `hidden_at` timestamp, which also gives us "when did I mute this"); if the
association is currently derived rather than stored, the brainstorm must decide
whether to materialize it or add a separate `session_hidden_tasks` table.

Sub-questions:
- `hidden_at` timestamp vs plain boolean — a timestamp costs nothing and lets
  `--only-hidden` sort by "muted longest ago", which is a plausible way to
  surface things you've been ignoring too long.
- Should a hide record survive the task leaving the session's list and coming
  back? (Probably yes — otherwise churn un-mutes things.)
- Does anything need to know the *reason* a task was hidden? A `--reason` on
  hide would show up in `--only-hidden` output and make the eventual unhide
  decision self-explanatory. Weigh against friction on what should be a
  one-keystroke action.

#### Discovery / unhide affordances to weigh

- `--only-hidden` — render the listing over the hidden set only.
- `--show-hidden` — render everything, hidden entries marked (dimmed, or a
  distinct glyph).
- A count/footer line on every normal render: `… 3 hidden (--show-hidden)`.
  This is the non-negotiable part: the indicator must appear by default.
- A `session hidden` subcommand that lists just the hidden set for the current
  (or a named) session.
- `unhide --all` for a session.
- Unhide by ordinal from the most recent `--only-hidden` render, so the user
  never has to retype a task ID.
- Cross-session view: "which sessions have hidden task E-NNNN?" — the inverse
  query, useful when a task finally resolves and you want to know whose lists
  it was muted from.

#### Auto-unhide policy — DECIDED: never

**Settled by Mike (2026-08-06): a hide never expires on its own.** No status
transition un-hides a task, including `unverified`. Hiding is a standing
statement that this task is not this session's concern; anything that
re-surfaces it reintroduces exactly the noise hiding was meant to remove. Only
an explicit unhide reverses it.

The synthesis records this as a decision, not as an option set. The only
remaining lifecycle sub-question is housekeeping: when a task reaches a terminal
status it drops off the list anyway, so its hide record becomes inert — decide
whether to garbage-collect those rows or simply leave them.

#### Non-goals

Hiding must not affect `task next`, blocking relations, landing, or any other
session's view. It is display-scoped, per-session, and nothing else.

Explicitly out of scope, per Mike: **anything that reasons about whether a
session is finished.** No archivability computation, no warnings about hidden
tasks at session exit, no gating. Endless surfaces the list; the human decides
when they're done with a session.

### Thread 3 — command placement

`session hide/unhide --task <id>` vs `task hide/unhide <id>`.

Given that hidden-ness is per-session state, the balance now favors **`session
hide/unhide --task <id>`**: the object being mutated is this session's view, the
task is the argument, and the command name doesn't imply a global property of
the task. `task hide <id>` reads like "hide this task" full stop, which is
precisely the wrong mental model and would invite the exact misreading it should
prevent.

Remaining questions for the synthesis:
- Does `session hide` default to the current session, with `--session <id>` to
  target another? (Needed for the "mute it in the other two sessions" workflow,
  which by definition is executed from *one* of them — or from outside any.)
- Should the task be positional (`session hide E-1234`) rather than behind
  `--task`, given it's the only required argument? Check against the existing
  CLI's conventions for other `session` subcommands.
- Multiple IDs per invocation?
- Discoverability: a user staring at `session status` wants that row gone and
  may reach for `task hide`. Mitigation is a footer hint naming the real command
  (`… 3 hidden — session unhide --task <id>`) rather than adding an alias.

## Deliverable

A written design proposal recorded as the task outcome, covering: the flag /
subcommand surface for both threads, the storage model for per-session
hidden-ness, the discovery/unhide affordances, and the icon+legend scheme for
`session list`, plus a settled recommendation for Thread 3. Auto-unhide is
already decided (never) and is recorded, not re-argued. No implementation in
this task — spin out implementation children and decisions once the design is
agreed.
