# Start task sessions early so they read in and wait

Strand 4 of the epic. Rewritten 2026-09-30: E-1993 landed and removed the
mechanism this plan was built on, and four sibling tasks landed pieces it
assumed it would build. What follows is what is actually left.

## The idea, unchanged

A task's implementation session starts before the user needs it. It reads the
plan and the code, records what it cannot answer, and then **stays live and
waits**. When the user is ready they resume that same session, context already
warm, with the questions already asked.

Two problems collapse at once. There is no evaluator-versus-implementer gap to
calibrate, because they are the same session — a predicted "this plan is
sufficient" and an actual one are the same judgment by the same context. And the
questions arrive while the user's own context on that task is still warm, which
is the scarce resource, not calendar time.

## What changed under this plan

**Triage is gone.** E-1993 removed it outright — the triage job, its sweep, the
`triage run` command, the `untriaged` status and the claim table. Every earlier
version of this plan said "triage spawns the session." There is no triage. The
trigger is new work, not a clause added to something existing.

E-1993 also landed three things this plan used to claim: an open question parks
a task, no plan means not claimable or spawnable, and the approval reset fires
on plan edits rather than description edits. Its no-plan refusal is the lazy
migration hook the old §6 described — what this task owes is what that refusal
leads to.

**Other siblings own more of it than they did.** Question storage is E-2176's
and this task only writes rows. The complexity and risk ratings exist; this task
only reads them. The session-state vocabulary exists, so `primed` is one more
row in it rather than a new concept. The changed-since-you-read-it notice
exists and only needs retargeting. The handoff renderer exists and needs one
clause. Auto-spawn eligibility and the spawn path itself are E-1814's, ready and
unstarted.

**Three sections of the old plan are retired.** Naming triage's facets is moot
with triage gone. The durable triage report has nothing to report unless the
priming run itself writes one — recast it as a record of each read-in, or drop
it. Integrating with auto-spawn shrinks to reusing E-1814's spawn path with a
different ending.

## The work that is still this task's

1. **The trigger.** On plan attach, and on E-1993's no-plan refusal, start that
   task's session with a handoff that ends: read the plan and the code, write
   what you cannot answer to the questions table, then stop and wait. The clause
   applies only when the trigger spawned the session, never when the user did.

2. **Plan drafting, behind a challenge call.** A planless task is where a
   read-in is most useful, because it can draft rather than merely refuse. One
   condition: a drafted plan passes an adversarial challenge before it reaches
   `submitted`. Without it the same agent authors and executes with only the
   user's approval between, which removes the separation this epic is built on —
   and a self-authored plan is self-consistent, so a bad one is harder to catch.
   The challenge is a one-shot headless model call: no window, no worktree, no
   claim, no session row. With the triage job gone, it needs a new home; say
   where in the implementation.

3. **A queryable `primed` state, and the session stays alive.** A primed session
   sitting idle is otherwise indistinguishable from a hung one. Liveness is not
   an artifact: a live session can receive messages and an exited one cannot,
   and E-1995's dispute resolution runs over exactly that channel. The cost is a
   live process and pane per primed session — comfortable in the tens, painful
   in the low hundreds — and is accepted for now. If it bites, the graceful fix
   is live-by-default with least-recently-needed eviction and resume from
   transcript. Do not build eviction now. The state display will need a third
   case.

4. **Resuming into a primed session, with a drift check.** Plan drift is not the
   risk — the paused session is the thing that updates the plan, so nothing
   rewrites it behind the session's back. Codebase drift is: between the read-in
   and the resume, possibly weeks, the code moves while the session's
   understanding does not. Re-check that what the plan cites still exists before
   proceeding.

5. **Type-aware sufficiency.** A brainstorm's framing counts as enough; a todo's
   does not. Absorbs the type-aware routing task.

6. **Retarget the changed-since-you-read-it marker** from description to plan,
   matching what E-1993 landed. Do not build a second notification path.

## Deliberately not here

Eviction. A second notification path. Anything that rebuilds question storage,
the ratings, the state vocabulary or the spawn path — all four exist. The
adversarial reviewer that asks "should this be built at all?" is a different
question, though it may share the challenge model call.

## Acceptance

- Attaching a plan to a task starts that task's session, which reads in, writes
  any open questions, and ends in `primed` without exiting.
- The no-plan refusal offers the same path, and taking it drafts a plan that
  reaches `submitted` only after the challenge call passes.
- `primed` is queryable, survives the session going idle, and renders distinctly
  from working and idle.
- Resuming a primed session re-checks that what the plan cites still exists and
  says so when it does not.
- A brainstorm with framing and no plan is not refused as planless; a todo is.
- The changed-since-you-read-it marker fires on plan edits, not description
  edits, and stays suppressed when the session itself made the change.
- A user-started session gets no stop-and-wait clause.
- `just test` and `just test-go` pass.


## Decided while implementing (Mike, 2026-10-01)

- **Trigger goes through the job runner.** Attaching a plan (the executor's
  unplanned→submitted inference, at creation or on update) sets
  `tasks.prime_requested`. A registered `prime` job — sibling of E-1814's
  auto-spawn job, reusing its target resolution and spawn seam — starts at most
  one primed session per interval via `endless task prime E-N --auto`.
  `task update` never opens a window itself.
- **Eligibility is opt-in.** Project config `prime.enabled` (default off, the
  kill switch) and `prime.cap` (default 3, outstanding primed sessions per
  project). Only `now`/`urgent` tasks no session ever bound, nothing
  non-terminal blocking, no open question.
- **Status stays `submitted`/`ready` while primed.** `task prime` creates the
  worktree and the session binds from its cwd, but nothing flips status. The
  resumed session's `task claim` is what moves it to `underway`: claim's
  "already yours" branch now still starts the task when its status is pre-work.
- **Framing = the `context` slot.** Brainstorm and research tasks with a
  non-empty context pass the plan gate; todo, bugfix and epic still need a plan.
- **Where the challenge lives:** `task update --plan-file` (Python), when the
  task had no plan and the updating session is the one bound to it — the only
  way that happens is a drafting prime, since claim and spawn refuse planless
  tasks. One-shot via `internal_claude.run_internal_claude`; a failed challenge
  refuses the attach and prints the objections.
- **`primed` state:** written by `endless session primed` (the handoff's last
  step), preserved by Stop, moved to `working` by the next UserPromptSubmit,
  which also runs the drift check — plan-cited paths missing from the worktree
  are named.

## As built — scope that grew during implementation

- **The drafter needs an explicit marker.** "The session bound to a planless
  task" was not enough: `task bind` can bind any session to one. `task prime`
  on a planless task opens the window with `@endless_prime_draft=<task id>`
  (spawn-window `--prime-draft`); the challenge runs only when that marker and
  the session's binding both name the task.
- **Primed windows open detached without the auto-spawn marker.** New
  spawn-window `--detached` flag, so a resumed primed task never counts against
  the auto-spawn cap.
- **`session primed` is pinned to the main database** in endless-go, beside
  `hook`: it writes the row the session's own hooks write.
- **Claim's re-claim branch now starts a pre-work task** its session already
  holds (any status but `underway`), which is how a resumed primed session
  begins work. A session re-claiming its own `revisit` task likewise flips it.
- **Description edits no longer notify** (migration 00013 rebuilds
  `tasks_notify_sessions`); plan, analysis and notes still do via the
  task_content triggers.
- `project status` gains a `◇ primed` rank between idle and doing.
- Drift check lives in `internal/plancite` (a heuristic tuned to miss rather
  than cry wolf: backticked tokens, or bare tokens with a file extension).

## After rebasing onto main (2026-10-04)

- `--detached` dropped: E-2234 landed spawn-window `--no-refocus`, the same
  act; `task prime` uses it, plus E-2234's `--placement` (the job passes
  `auto_spawn`'s placement setting through a hidden `--placement`).
- E-2156 rebuilt `project status` around tasks, so the primed rank moved: a new
  `taskrow.Primed` action (◇ primed) and a `Primed` flag on the project-status
  row; a task held by a primed session reads ◇ instead of its ⚑/▶/✎ status.
- E-2159's refusal classes: every new refusal is `no_report`/`report`/`relay`
  (Python) or `internal/refusal` (Go `session-prime`).
- No migration renumber was needed: main's 00011/00012 are the same files this
  branch already had; 00013_prime is next.
- The Endless project opts in: `"prime": {"enabled": true}` in its
  `.endless/config.json`.
