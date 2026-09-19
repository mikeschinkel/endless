# Largely subsumed by write-once `sessions.task_id` (E-1969 and the tightening)

Re-analysed 2026-08-16 after the requester pointed out that a bind which can
never be reassigned is the FEATURE, not the failure. They are right, and the
original framing of this task was wrong.

## Why write-once fixes the reported symptom

`session resume E-1953` resumes an EXISTING Claude session by UUID, so the
`sessions` row already exists with `task_id = 1953`. `trySpawnBind` firing at
SessionStart is therefore a REASSIGNMENT of an already-bound session, not a
first bind. Under write-once semantics that write is refused and E-1953 stands.
No marker change needed for this case.

That is the entirety of the reported incident (twice), so the tightening
resolves it.

## The residual case, which write-once makes WORSE rather than better

A genuinely NEW Claude session started in a window left over from a previous
spawn. There the stale marker is the FIRST write, so write-once cements the
wrong task permanently — and the manual `task bind` recovery is gone by design.

Scope of that risk, measured rather than assumed:

- `task spawn` always creates a FRESH tmux window (`new-window`) and sets
  `@endless_spawned_by`, `@endless_task_id`, `@endless_project_id` on it
  (spawnlaunchcmd/tmux_driver.go, windowOptionCommands).
- Nothing anywhere clears any of the three.
- So the markers are per-window and die with the window. Exposure is confined
  to a spawned window that OUTLIVES its session and is then reused for
  something else.

That is narrow, but it is not theoretical: it is exactly the shape of the
incident that produced this task, differing only in whether the session was
resumed or fresh.

## Recommended shape, if it is still worth doing

CORRECTION (2026-08-16): an earlier draft of this analysis said not to clear the
window variables because it would break the E-1700 read race. That was wrong.
E-1700's race is `@endless_task_id` reading EMPTY, in which case `trySpawnBind`
returns false and binds nothing — so the variables must survive that path for
the retry, and they do. Clearing only after a bind SUCCEEDS preserves the retry
entirely.

So the fix is the obvious one: **`trySpawnBind` clears `@endless_spawned_by`
and `@endless_task_id` once it has successfully bound.** One spawn, one bind.
A later session starting in the same window finds nothing and falls through to
the cwd-based bind, which is correct for it.

Recording an expected session identity at spawn time is NOT viable as an
alternative: `task spawn` launches `claude` and the Claude session UUID does not
exist until the process starts, so spawn has nothing to record.

## Sequencing

Blocked on the write-once change rather than duplicating it. If that lands
first, re-scope this to the residual case only; the title should change with it,
since "clear the markers" is no longer the recommended fix.

## Requester position (2026-08-16): enforce the invariant, don't tolerate the breach

`tmux window == Claude session == Endless task` is to be treated as an
invariant. A new Claude session starting in a window left over from a previous
spawn should be made to FAIL, not quietly bound to something. Allowing the
invariant to break cascades: the tmux status line already resolves by WINDOW,
not pane, so a window associated with two sessions is ambiguous to it
independently of any binding bug.

Two mechanical facts that constrain how this can be built:

1. **SessionStart cannot hard-refuse a session.** The hook's only lever there is
   `additionalContext` — `handleWorktreeAdoption` already returns a "refusal"
   that is nothing more than injected text. Making a session genuinely unusable
   requires blocking at PreToolUse, which is the shape `enforceWorktreeGate` and
   `enforceClaimedCwd` already use. So "fail to run" = inform at SessionStart +
   block tool calls at PreToolUse.

2. **E-1669 faced this choice and went the other way**, deliberately: "A
   warning, NOT a refuse: refusing in the hook path blocks every tool call."
   That was the right call for its risk (a session dogfooding the wrong binary).
   The risk here is different in kind — under write-once `task_id` a mis-bind is
   permanent and has no recovery — which is what justifies the stronger
   response rather than copying E-1669's.

**Ship an escape hatch with the gate, or the gate becomes the bug.** Nothing in
the codebase clears `@endless_spawned_by` / `@endless_task_id` /
`@endless_project_id` today. If a window can be refused on the strength of those
markers, there must be a supported way to reset them — otherwise the first false
positive strands a tmux window permanently and the only workaround a user will
find is killing the window or disabling the gate.

Relationship to the identity-scoping fix above: they do different jobs and both
are worth having. Identity-scoped markers stop the WRONG BIND (a fresh session
falls through to the cwd bind, which is correct). The refusal enforces the
INVARIANT, which matters beyond binding because window-keyed resolution exists
elsewhere. Scoping is the narrower, safer change; refusal is the architectural
one and carries the false-positive risk.

---

# Addendum — E-1969 landed; re-scoped to the residual case (2026-08-25)

VOCABULARY. The sections above call them "markers". They are tmux WINDOW
OPTIONS — key/value pairs tmux stores per window, readable with
`tmux show-options -w` and by `tmux display-message -p -t <pane> '#{@name}'`.
`task spawn` sets three of them on the window it creates; a fourth is written by
the hook. A spawned window looks like this:

    @endless_project_id     1
    @endless_session_uuid   f4374aaf-b432-495a-b377-351068e8dfb8
    @endless_spawned_by     1137
    @endless_task_id        1969

Below this line the term "window option" is used instead of "marker".

Written from ES's E-1969 session at Mike's request. Evidence and scope only; the
choice between the two shapes below is still Mike's to
settle.

## The reported symptom is now fixed, in the database

`sessions.task_id` is write-once as of E-1969, enforced by the
`sessions_task_id_write_once` trigger. The resume case plays out like this now,
verified against the shipped code:

1. `trySpawnBind` reads the stale `@endless_task_id` and calls
   `BindSessionToTask`.
2. The row already holds a task, so the write is a reassignment and the trigger
   aborts it. `BindSessionToTask` returns an error naming "write-once".
3. `trySpawnBind` logs and returns FALSE, so `maybeCwdBind` runs
   `autoBindFromCwd`, which E-1856 already guards against re-pointing.
4. The correct binding stands.

No marker change is needed for that case. `session resume E-1953` can no longer
be stolen by a leftover marker.

## What is left, and why write-once makes it worse

A genuinely NEW session in a window a previous spawn left behind. `task_id` is
NULL, so the stale marker is the FIRST write and the trigger PERMITS it. The
wrong task is then permanent: E-1968 made `task bind` first-set-only and E-1969
enforces it in the database, so the manual recovery this task's original
incident used is gone by design.

Before E-1969 this was a recoverable annoyance. After it, it is unrecoverable.
That inversion is the whole remaining case, and it is the reason to keep this
task open rather than close it as subsumed.

## Defect in the recommended shape above — do not build it as written

The "Recommended shape" section says `trySpawnBind` should clear
`@endless_spawned_by` AND `@endless_task_id` once it has bound. Clearing
`@endless_spawned_by` is wrong: it is not only a bind trigger.
`monitor.ResolveSessionStatusParentSession` reads that same window option to
render the `from` provenance row in `session status`
(`internal/monitor/session_status.go`, and the note at
`internal/sessionstatuscmd/session_status.go` about keeping that row testable
headless). Clearing it at the first SessionStart erases the window's parent-
session link for the rest of its life.

CORRECTION, same day: the two paragraphs that stood here proposed unsetting
`@endless_task_id` instead, and a new `@endless_bound_session` option as a
variant. Both were wrong. See "Unsetting any marker is out" and "The window
already carries the facts" below, which replace them. Recorded rather than
deleted because the reasoning that produced them is the thing to avoid
repeating.

E-1700's retry is unaffected by any of this and is worth keeping straight: that
race is `@endless_task_id` reading EMPTY, in which case `trySpawnBind` returns
false and binds nothing, so the next SessionStart retries.

## Unsetting a window option is out

Not "clear `@endless_spawned_by` but `@endless_task_id` is fine" — unsetting is
wrong for either one, for two independent reasons.

### It destroys a value that other readers depend on

`@endless_task_id` is not only `trySpawnBind`'s trigger.

`endless session status` has to decide which task the view is ABOUT. It tries
three sources in order, and stops at the first that answers: (1) the task held
by a live session in this window; (2) the window's own `@endless_task_id`;
(3) the most-recent live session. Source (2) is the one at issue — it is what
answers when no live session is found, which is exactly the state a window is in
after its spawned session ends. Unset it and `session status` in that window
loses its subject.

E-1302 (`endless task id` / `endless tmux task`, spawned 2026-08-25) proposes
reading the same window option as a user-facing lookup, so it would lose its
answer too.

### It conflates two states that must stay distinct

Absent has to mean one thing, and unsetting would give it two:

- **(a) This window WAS created by `task spawn`, and its one bind already
  happened.** The state unsetting is trying to record.
- **(b) This window was never created by `task spawn` at all.** The ordinary
  case: a tmux window opened by hand, `claude` started in it. `task spawn` never
  touched it, so it has no `@endless_*` options and never did.

Both read as "no `@endless_task_id`". Nothing downstream can tell them apart.

Whether that matters depends on what is being built, which is why it is decisive
here. For "just do not bind twice" it does not — (a) and (b) both want the same
outcome, fall through to the cwd bind. For a gate that REFUSES, it is fatal:
(a) is the breach to refuse and (b) is a completely ordinary window that must
never be refused. Unsetting destroys precisely the distinction the gate is made
of.

Encoding a fact by destroying the evidence for it is also the class of write
E-1898 removed from `sessions`, and for the same reason: the erased value is
what you need when the thing goes wrong.

## The window already carries the facts — the question is which id is durable

A spawned window carries four options today:

    @endless_project_id    @endless_spawned_by
    @endless_task_id       @endless_session_uuid

`@endless_session_uuid` is E-1585's, written by `setTmuxSessionUUID` in
`internal/hookcmd/claude.go`, so no new option needs inventing to record "which
session is in this window".

Two facts about it that constrain how it can be used, and one conclusion.

**Today it is overwritten on EVERY event**, before the SessionStart handling
that would read it — deliberately, so it self-heals after a tmux server restart.
As written it therefore records the most recent session in the window, not the
one the window was spawned for.

**Freezing it would be the wrong fix.** The value is a harness-issued
conversation id, and E-2063 measured that a clear rotates it (resume and
compaction do not). Making a rotating handle sticky does not turn it into an
identity; it turns it into a stale handle, and it breaks what E-1585 built the
option FOR — a sibling shell pane reading it to discover the Claude session
beside it would resolve a conversation that no longer exists. E-2063 names this
error class directly, in rejecting `CLAUDE_PID` as an anchor: "the general error
was reaching for a RUNTIME HANDLE as a DURABLE IDENTITY."

**So publish the id Endless owns instead.** Once E-2063 exists that is
`sessions.id`, which never rotates — at which point overwrite-vs-write-once
stops being a question at all, because writing the same value every event is
idempotent. The option stays self-healing AND stable across a clear, with no
invariant to enforce. This is the same move E-1969 made one table over:
`sessions.task_id` is write-once because Endless mints it; `sessions.session_id`
is not that kind of id, which is why E-2063 moves it out rather than freezing
it.

## E-1128 wants the opposite of a clear

E-1128 ("Record spawn lineage on `endless task spawn`", unplanned) treats
`@endless_spawned_by` as durable lineage. Partly stale — the marker exists today
and is already read for the provenance row — but whichever branch is chosen here
must not erase it, or E-1128's premise goes with it.

## Why Branch B is not merely a bigger Branch A

`monitor.GetTaskForPane` resolves pane-specific first, then falls back to WINDOW
scope: any pane in the same window with a non-NULL `task_id`, most recent
`last_activity` winning. So a window that has hosted two sessions on two tasks
gives the status line an answer decided by a timestamp race, independently of
whether either binding was correct. Marker scoping does not touch that; the
invariant gate is what would. Branch A fixes an instance, Branch B fixes a
class. That is the real difference between them, and it is not visible from the
binding bug alone.

## Sequencing note

E-1972 (underway) may invert which binary runs the Claude hooks. Whatever lands
here lives in `internal/hookcmd/claude.go` on the SessionStart path, so it will
sit underneath that decision. Not a conflict — overlap to coordinate, in that
order.

---

# The invariant, as the requester states it (2026-08-25)

Superseding the 2026-08-16 "Requester position" section above, which said
`tmux window == Claude session == Endless task`. That form cannot hold, because
a clear mints a new harness session id. The form that does:

    tmux window == Endless task == one or more Claude sessions

with two explicit non-accommodations:

- NOT multiple CONCURRENT sessions for the same task.
- NOT multiple ACTIVE tmux windows for the same task.

Sessions in SERIES are accommodated; sessions in PARALLEL are not. That is the
whole of it, and it is what makes the gate expressible: the thing being counted
is the Endless session, not the harness conversation.

# Decided 2026-08-25: two window options, two jobs

- **`@endless_session_uuid` keeps its current behaviour** — overwritten on every
  event, meaning "the CURRENT Claude session in this window". That is correct for
  what E-1585 built it for and must not be frozen.
- **Add `@endless_session_id`, carrying `sessions.id`** — the durable identity
  Endless mints and owns. It is the one a gate may compare against.

Two options because they answer two different questions: which conversation is
live in this window right now, and which Endless session this window belongs to.
Neither can do the other's job.

# The one question left

Everything else above is settled. What is not:

**When the window says task T and the starting session's cwd says task M, which
source wins — and is the disagreement resolved silently or refused?**

That is the whole of it, and every earlier framing of this task collapses into
it. The three answers are the three behaviours that have been discussed:

- **Window wins.** Today's behaviour, and the bug: `trySpawnBind` binds to T,
  returns true, and suppresses the cwd bind that would have said M. Under
  write-once that is now permanent.
- **cwd wins.** `trySpawnBind` defers when the two disagree; `autoBindFromCwd`
  binds M. Silent, small, and correct for the ordinary case. Reaches the same
  end state the old "clear the markers" shape aimed at, with nothing unset.
- **Refuse.** The disagreement is the invariant breach; the session is informed
  at SessionStart and blocked at PreToolUse. Enforces the invariant rather than
  papering over it, and costs a false-positive budget plus an escape hatch.

Why it is one question and not two: "what does it compare" is already answered —
`@endless_task_id` against `resolveCwdTaskID`, which is the signal
`autoBindFromCwd` already trusts, and which needs no window option to be
mutated. Only the policy on mismatch is open.

Two sub-cases the chosen answer has to cover, neither of which changes the shape
of the question:

- **cwd is not in any worktree** (the main checkout, or anywhere else). The cwd
  source has no answer, so there is no disagreement to detect — only the window's
  claim. Refusing here would refuse a great many ordinary windows.
- **After E-2063, the session may be a new INSTANCE of the window's own Endless
  session** (a clear). That is not a disagreement at all and must never be
  refused; `payload.Source` distinguishes it today (`clear` vs `startup`), and
  E-2063 may make it structural.

Independent of the answer: nothing clears any of the four window options, so a
window refused on their strength has no supported reset. If the answer is
"refuse", the reset ships with it or the first false positive strands the window.

# Corrections to the addendum above, for the record

- The addendum's "Defect in the recommended shape" originally concluded that
  `@endless_task_id` could be consumed by unsetting. Replaced in place; see
  "Unsetting any marker is out".
- An earlier draft of this analysis cited E-1640 as evidence that a succession
  of session rows per window is normal and supported. That inverts what E-1640
  says: it is titled "Fix duplicate session rows minted per Claude launch when
  TMUX_PANE is empty" and its remedy ENDS the extra rows. It treated the
  succession as a defect to suppress, which is the opposite of blessing it.
  E-2063 is where that model actually changes.
- The addendum used the word "marker" throughout without ever defining it. It
  means an `@endless_*` tmux window option; see the VOCABULARY note at the head
  of the addendum. New text says "window option".

# Scope added from E-2063: repairing the rows this bug already created

Rolled in per ED-1550(4) — this task owns the area, so the cleanup belongs here
rather than in a task chained behind it.

E-2063's planning session measured the damage. 45 tasks carry more than one
`sessions` row, covering 127 rows. Splitting them by the working directory each
row's first hook event recorded (`activity.working_dir`, which is the only
durable record of where a session was launched):

| | tasks |
|---|---|
| every row shares one launch directory — plausibly genuine clears | 20 |
| rows launched from DIFFERENT directories — at least one is not a clear | 25 |

The 25 are this task's defect. The 20 are E-2063's. Both populations need the
same repair — decide which rows are instances of one Endless session and which
are separate sessions that were mis-bound — so the repair is done once, here,
after both mechanisms are fixed. E-2063 deliberately does NOT collapse them:
going forward a clear appends an instance, and the historical rows are inert
(every row but one per task is `ended`), so nothing operational depends on the
cleanup happening first.

## E-1732: a worked example of the mixed case

Three `sessions` rows, minted within half an hour, which an earlier reading took
for consecutive clears. The transcripts and `activity.working_dir` agree that
they were not:

| session | launch cwd | branch | lines | span |
|---|---|---|---|---|
| ES-879 `26ba3f7d` | worktree `e-1732` | `task/1732-…` | 292 | 05:04:35 → 05:28:32 |
| ES-881 `ee6eda74` | main checkout | `main` | 13 | 05:29:54 → 05:30:15 |
| ES-882 `9f45bd76` | main checkout | `main` | 801 | 05:30:18 → still live |

ES-879 records the worktree cwd on all 292 of its lines and never the main
checkout, so a clear from it would write the next transcript under the
worktree's slug directory. ES-881 and ES-882 are under the MAIN checkout's slug,
on branch `main`. Two `claude` launches in the main checkout, bound to E-1732
without ever entering its worktree — this bug, not a clear.

(ES-882 does show the worktree cwd on lines dated 2026-08-27, eight weeks after
it started. That is the E-2080 session `/cd`-ing into the worktree while
investigating. A `/cd` changes the per-line cwd but does not move the transcript
file, which stays under the slug of the directory the session was LAUNCHED in.
Only tool-result overflow files follow the new slug. This is the trap that made
the lineage look like clears.)

The damage is worse than a duplicate row: all of E-1732's actual work is on
ES-879, which reads `ended`, while ES-882 — the row that is `working`, the row
`task show` surfaces — holds no conversation at all. So a repair that keeps the
newest row and discards the rest would destroy the history and keep the husk.

## What the repair needs

- The discriminator is the launch directory, taken from the FIRST
  `activity.working_dir` for each row's UUID. Same directory as the surviving
  row → an instance of it. Different directory → a separate session that was
  mis-bound; unbind it rather than folding it in.
- Never keep the newest row. Keep the one the conversation is attached to.
- Fold nothing in silently: 25 of 45 need a judgment call, so the repair prints
  what it decided per task and is re-runnable.
