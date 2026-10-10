# Endless error codes

Every classified fault Endless records carries a stable code. This page is the
catalog: what each code means, why it fires, and what to do about it.

**The prefix states the severity.** `WARN-NNNN` is a warning — something is
degraded and still working. `ERR-NNNN` is an error — something you asked for did
not happen. Severity is a property of the code and never of the call site, so
two places raising the same condition cannot disagree about which colour you
see; putting it in the id is what lets every display stop spelling the word out
beside the code.

That was not always true. `ERR-0001` was severity *warning* from the day it
shipped until E-2148 renamed it `WARN-0001` — an id that stated one thing and
meant another.

**Numbers are never reused, and re-prefixing did not move any.** `ERR-0001`
became `WARN-0001`, not `WARN-0006`. A number is spent the moment it ships:
incidents in your database, lines in the detail log and text in old bug reports
all cite one, and renumbering would make every one of them point at a different
code. Seven of the fourteen changed prefix; a schema change rewrote `errors.code`
for the incidents already recorded, so they still resolve to a catalog entry.
The detail log was deliberately left alone — a line reading `ERR-0001` is an
accurate record of what the code was called when that occurrence was captured.

There is still no **subsystem** prefix (`JOB-`, `HOOK-`, `DB-`): that would squat
on identifier namespace project-scoped task IDs may want — task IDs are `E-NNNN`
today and may become per-project prefixes later — and which subsystem raised a
fault is already recorded in its **source** field. Severity is different on both
counts: there are exactly two values, neither can be mistaken for a task id, and
no other field states it.

Retiring a code spends its number permanently.

## Seeing and clearing errors

```sh
endless errors list              # open incidents in the project you are in
endless errors list --all        # include cleared ones (history)
endless errors show 12           # ONE incident in full, with its remedy
endless errors show 12 --detail  # and every logged occurrence
endless errors clear             # mark every open incident cleared
endless errors clear 12 13       # clear specific incidents
endless errors clear --log       # dismiss only what is waiting in the log
endless errors codes             # print this catalog from the running binary
endless errors accept 12         # (a session) error 12 is mine to fix
endless errors decline 12 --reason "..."  # (a session) it is not
endless errors escalate 12       # route error 12 to a session now
```

**`list` lists; `show` shows one.** The listing has one line per incident and
truncates each summary to the width it has; `show <id>` is where the whole
summary lives, along with **what to do about it** — the same "What to do"
paragraph this page carries for that code, printed by the command rather than
left for you to come and find. `show` used to be the listing verb, which made it
the only `show` in the CLI that did not mean what `task show` and
`decision show` mean. `errors show` with no id is now a usage error naming
`list`, never a listing.

Until E-2148 nothing anywhere told you how to resolve anything. The listing's
footer explained how to *dismiss* an incident, and was careful to say dismissing
is not a retry — so the one action the surface named was the one that changes
nothing.

`session status` and `session monitor` append a trailing **fault row** whenever
open incidents exist — max severity wins, `error` outranks `warning`:

```
[ WARN-0004 ] job scheduling row could not be created       Run eeh
[ ✕2 ⚠1 ] ERR-0002 ERR-0011 WARN-0004                       Run eeh
```

An inverted chip on the left, then text, then `Run eeh`. One open incident puts
its **code** in the chip and its summary beside it; several put a severity
**tally** in glyphs there and then the distinct codes, most severe first. The
chip carries no severity *word* — the code says it — and the colour of the whole
row carries the highest severity open.

Two behaviors worth knowing:

- **Clearing never deletes.** The row stays as history. A recurrence of the same
  fault opens a *new* incident beside the cleared one, so a problem that came
  back is visibly distinct from one that never left — which is what lets you
  pinpoint when a regression landed.
- **Clearing is not retrying.** `errors clear` means "I have seen this".
  Making a backed-off job due again is `endless jobs retry <name>`, deliberately
  a separate verb, so tidying your error list cannot silently re-arm a job that
  is still broken.

## Which project an error belongs to

One Endless database holds every project on your machine, so every fault records
the project it happened in. `show` and `clear` are scoped to the project
enclosing your working directory:

```sh
endless errors list                      # this project (plus the machine's own)
endless errors list --project acme       # another project
endless errors list --all-projects       # everything, with a PROJECT column
endless errors clear --all-projects      # dismiss every open incident, everywhere
```

Both flags work the same on `clear`, and they matter there: with no ids, `clear`
dismisses exactly the set a `list` under the same flags would show. Naming ids
overrides the scope — `errors clear 12` clears incident 12 whichever project it
belongs to, because you named it. `errors show 12` is the same rule: you named
the row, so there is nothing left for a scope to decide.

**Every scope also includes the errors that belong to no project.** Some failures
are the machine's, not a project's: the background job runner unable to open the
database, the tmux status bar unable to resolve a pane. Those have no project to
be filed under, so they ride along with whichever project you ask about — they
would otherwise be visible on no default view at all. In an `--all-projects`
listing their PROJECT column reads `—`.

Run outside any registered project and there is nothing to scope to, so both
verbs cover the whole machine; the listing's header says so in as many words.

**A listing always says what it counted.** Every `errors list` opens with a
header naming the scope and the count — including when the count is zero, and
especially then:

```
no errors in endless — 2 elsewhere (endless errors list --all-projects)
no errors in endless
3 errors in endless
2 errors across every project
1 error across every project (no project encloses this directory)
```

That first line is why this exists. `errors show` used to print a bare
`no errors` from inside one project while the `session status` fault row
simultaneously reported `1 error, 1 warning`, because both incidents belonged to
a *different* project. Neither surface was lying — the listing meant "none here"
and said "none" — but two surfaces disagreeing about whether anything is wrong
is worse than either answer alone. The listing stays scoped; it just stops
saying "nothing" when it means "nothing here".

The same rule scopes the fault row: `project status` and `project monitor` count
their own project's open incidents plus the unattributed ones, while
`session status` and `session monitor` stay machine-wide — they render every live
session on the box, whatever project each is in.

## Who raised an error

Every occurrence also records **who raised it**: the task and the session that
were active. `errors list` shows the latest raiser in a `BY` column, and a `+N`
when N others raised the same incident; `errors show` lists every one of them,
most recent first:

```
ID  CODE       COUNT  BY                   LAST SEEN            SUMMARY
12  WARN-0001  41     ES-1299 (E-2259) +2  2026-10-07T14:02:11  job "rater" failed: …

Raised by:
  ES-1299 (E-2259)  38 occurrence(s), first 2026-10-07T09:12:40, last 2026-10-07T14:02:11
  ES-1302 (E-2268)  2 occurrence(s), first …
  E-2270            1 occurrence(s), first …
```

`ES-N (E-N)` is a session and the task it was working on; `E-N` alone is a task
with no session; `-` means neither could be told. `show --detail` adds each
occurrence's own raiser.

- **A session is recorded only when an agent ran the command.** A hook, or a
  command run under an agent harness, names its session; a command you type in
  your own shell names none, even inside a tmux window beside a session or
  after `esu`. The session is what an error would be routed back to, and an
  error you caused must not be routed to an agent.
- **The task** is the one the fault is about when its producer knows it (the
  worktree reaper, the unsettled probe, a failed rating), else the session's
  task, else the task whose worktree the command ran in.
- **A background job's own failure names no one** unless it is about one task:
  nothing was working on anything when it ran.

The incident keeps only its latest raiser — the one to route to. One row per
distinct raiser is kept beside it, so `+2` means three sessions or tasks hit the
same fault, not three occurrences. Errors recorded before raisers were tracked
show `-`.

## Routing an error to a session that fixes it

A project can opt in to the **fault-triage job** (`fault-triage` in `endless jobs
list`), so work on an error starts as soon as it surfaces. It is off until the
project's own `.endless/config.json` turns it on:

```json
{ "fault_triage": { "enabled": true } }
```

Only errors first seen after the job first notices the opt-in are routed:
opting in never triages a backlog. `fault_triage.interval` in
`~/.config/endless/config.json` sets the cadence (default `1m`).

Each new error goes one of four ways, decided by who raised it (above):

- **A live session raised it.** Once Endless's hook-recorded state says that
  session is idle, it is sent a message: *we think error N is yours*. It answers
  with `endless errors accept N` (it is mine; I will fix it in my own task) or
  `endless errors decline N --reason "..."`. A busy session is never
  interrupted unless you run `endless errors escalate N`.
- **The session that raised it has ended,** and its Claude transcript still
  exists: it is resumed in a new tmux window that does not take your focus,
  with the message as its first prompt. Its task is left alone; a session that
  accepts sets its own task to `revisit`.
- **Otherwise** — nobody to ask, a decline, a session that went idle again
  without answering, a message that could not be delivered — a **bugfix task**
  is filed and spawned. Its context carries the error's record, who raised it,
  why it landed there and its latest captures; its plan starts with accepting
  the error. There is one task per fingerprint: a recurrence joins the open
  task rather than filing another, and one whose task has already settled files
  a new task that cleans up the old one.
- **A fix session raised it** (a session on a bugfix task this job filed): it
  is recorded on that task and never spawned, so a fix cannot spawn a fix for
  itself.

Starting sessions is throttled: **one outstanding at a time**, machine-wide. No
session is resumed or spawned while a resumed one has not answered or a spawned
fix task has not reached `unverified`. Messages are not throttled. A session is
started only while a tmux client is attached, into the tmux session and tab
position that `auto_spawn.target` and `auto_spawn.placement` name.

```sh
endless errors accept 12                      # this session takes error 12
endless errors decline 12 --reason "the rater raised it, not my change"
endless errors escalate 12                    # route it now; don't wait for idle
endless session goto --error-fix 12           # go to the session fixing it
```

`errors list` marks an accepted error with `✓` in a column of its own (shown
only when some listed error has one); `errors show` names who accepted or
declined it, its fix task, and where triage has it.

**Delivery to a live session** goes through a throwaway headless Claude:
`claude -p` allowed only `SendMessage`, `ListAgents` and `ToolSearch`, run from
`<config-dir>/endless-triage` (so the message is seen to come from
`endless-triage`), with hooks off, finding its target by tmux pane. It needs
**Claude Code's cross-session `SendMessage`**: verified on Claude Code 2.1.293,
and present in 2.1.285, the oldest build it has been checked against; older
builds are untested. A message the sender reports as held, or cannot place,
counts as undelivered and the error is filed instead.

Two limits worth knowing:

- A session working on **no task** stays `idle` in Endless's state while it
  works, so the job waits five minutes after messaging any session before
  reading an idle session as having moved on without answering.
- The routing state is **machine-local**, like the rest of this record: it is
  never written to the ledger.

## Where the detail lives

The `errors` table holds only the index — project, code, source, summary, counts.
Each occurrence's full capture (stack traces, command output, the job's own log
output) is appended to `<config-dir>/log/errors.jsonl` and read back by
`errors show <id> --detail`. The table therefore stays bounded by the number of
*distinct* faults rather than by how often they happen, and nothing is lost to
diagnosis.

Detail lines carry the project by NAME (`"project": "acme"`), because that file
is read without a database — one log holds every project on the machine. Lines
written before projects were recorded simply have no `project` key; nothing
rewrites them, since a fault's project cannot be reconstructed after the fact.
They carry the raiser as ids (`"task_id": 2259, "session_id": 1299`), each
omitted when not known.

That file is machine-local. It is not the shareable db-ledger, it is never
replayed into the database, and faults emit no ledger events.

### When the database is what failed

A fault raised *because* the database could not be written cannot be recorded
in it. The detail line is still written — that is the half of this that does not
need a database — and it says so: `"fault_id": null` and `"log_only": true`,
with a `db_error` giving the reason. No id is invented for it, because an id
is what `show <id>` and `clear <id>` address a row by, and a fake one that later
collides with a real one is worse than an honest absence. Lines written before
E-2288 carry `"unindexed": true` and `index_error` instead; they are listed and
cleared exactly as the new spelling is.

Those occurrences are not silent. `errors list` prints them beneath the table,
without ids, under `N occurrence(s) written to log only; DB write failed:` with
each reason prefixed `not in the database:`, and falls back to printing them
alone when the table itself cannot be read. The fault row on `session status`
adds one line — `✕ N errors written to log only`, or `the error record could
not be read` — so the surface whose job is to say something is wrong is not
blind to the case where the thing that is wrong is the fault store.

#### Dismissing them

`endless errors clear --log` dismisses them and touches nothing else. That
separation is the point: the two halves of this record are acknowledged for
different reasons, and `--log` is the form that still works when the database
is the thing that broke — a plain `clear` has to fail on its table half there.
A plain `errors clear` with no id dismisses both, because that is what "dismiss
what you just showed me" means when the listing printed both. Clearing a
*specific* id never touches the log: an id names a table row, and these have
none, so `clear --log 12` is refused rather than guessed at.

They are dismissed by a watermark beside the log rather than by a `cleared_at`
column, since that column is unreachable in exactly the state this exists for.
The watermark carries a digest of the log it was measured against, so a file
rotated or restored under it is detected rather than silently skipped.

One thing `clear` deliberately does **not** silence: `the error record could
not be read`. That line is not a stored report — it is re-derived on every
render from whether the database opens — so there is nothing to mark seen, and
suppressing it would mean muting a failure that is still happening. It goes
away when the database does.

---

## WARN-0001 — job-failed

**Severity:** warning · **Raised by:** the background job runner

A registered background job's `Run` returned an error.

This is a warning rather than an error because the runner recovers: it records
the failure, releases the lease, and reschedules the job. One failure is not yet
a broken system.

**What to do.** Read the detail (`endless errors show <n> --detail`) — it
carries the error and anything the job logged. If the cause is transient, the
job will retry on its own cadence. If the job declares a `MaxBackoff`, repeated
failures push its next attempt exponentially further out, up to that cap; fix the
cause and run `endless jobs retry <name>` rather than waiting the backoff out.

## ERR-0002 — job-panicked

**Severity:** error · **Raised by:** the background job runner

A background job panicked. The runner recovered it; the panic value and full
stack are in the detail log.

This is an error, not a warning: a panic is a bug in the job rather than an
expected failure mode. It also means the job was one `recover()` away from
killing its host process — for the session-monitor trigger, that is the user's
live dashboard disappearing back to a shell prompt.

**What to do.** Treat it as a bug in the job. The stack in the detail log names
the line. The job's scheduling row is intact and it will be retried.

## ERR-0003 — job-timed-out

**Severity:** error · **Raised by:** the background job runner

A job outran its lease TTL and had its context cancelled.

**What to do.** Either the job is slower than its `Schedule.LeaseTTL` allows, or
it is wedged. Raise `LeaseTTL` if the work legitimately takes that long — the TTL
must exceed the job's realistic worst case, because once it expires another
invocation may claim and run the job concurrently.

Note that Go cannot forcibly stop a goroutine that ignores its context. A job
that does not honor `ctx` will leak a goroutine in a long-running trigger
process even after this fault is recorded.

## WARN-0004 — job-scheduling

**Severity:** warning · **Raised by:** the background job runner

The runner could not read or write a job's scheduling row: the database was
unavailable, locked beyond the busy timeout, or missing the `jobs` table.

No job state is corrupted — every scheduling write is a single statement — and
the next invocation retries. A persistent occurrence means something is wrong
with the database itself rather than with any job.

Creating the scheduling row retries contention in place before raising this
(E-1950): three attempts, each behind the connection's five-second
`busy_timeout`. Losing a single lock race is what a database with concurrent
writers does, not a fault, and this code is only raised once losing it has
stopped being explicable that way. Errors that are *not* contention — a missing
table, a disk failure — are raised immediately without retrying.

**What to do.** Check that the database is reachable and that the schema is
current. A schema version mismatch is raised as ERR-0020 rather than here, and a
worktree build is refused the main database outright (ED-1601), so neither is
the cause of this code.

## WARN-0005 — job-stuck-lease

**Severity:** warning · **Raised by:** the background job runner

A job finished, but by then its lease had already been re-claimed by another
invocation — so two invocations may have run it concurrently.

**What to do.** Raise the job's `Schedule.LeaseTTL` above its realistic
worst-case runtime. Also confirm the job is genuinely idempotent: the lease is
time-boxed rather than an OS lock precisely so a dead process needs no cleanup,
and the unavoidable cost of that design is that a slow job can be re-entered.

## WARN-0006 — test-warning

**Severity:** warning · **Raised by:** `endless errors raise`

Nothing is wrong. This code exists only so the error surface can be exercised on
demand — the fault row on `session status`, the `errors list` listing, the JSONL
detail log — without waiting for something to genuinely break (E-1950).

```bash
endless errors raise                          # a synthetic warning
endless errors raise --severity error         # a synthetic error (ERR-0007)
endless errors raise --repeat 4               # one incident, four occurrences
endless errors raise --summary "custom text"  # override the summary
```

It records through the same path a real fault takes — same upsert, same
fingerprinting, same detail line — so what you are looking at is shaped exactly
like the real thing. Only the code marks it synthetic.

Inside a self-dev worktree it requires an explicit `--db main|sandbox`, like
every other `errors` verb, and refuses without one — so a synthetic fault cannot
land in a record you did not mean to touch. Use `--db main` to see it on the
`session status` fault row, which reads main.

**What to do.** Dismiss it: `endless errors clear <id>`. If you did not raise it
yourself, someone was testing; it is not a fault report.

## ERR-0007 — test-error

**Severity:** error · **Raised by:** `endless errors raise --severity error`

The error-severity counterpart to WARN-0006, for exercising the surfaces that
treat `error` differently from `warning` — the red fault-row styling and the
max-severity precedence.

**What to do.** Dismiss it: `endless errors clear <id>`.
## ERR-0008 — status-line-unavailable

**Severity:** error · **Raised by:** `endless-go tmux status-line`

The tmux status line could not resolve what to show for a pane, and rendered
its dim placeholder instead.

This is an error rather than a warning because of how it *looks*: the
placeholder is byte-identical to "this pane has no Endless context", so a
broken bar and an empty bar are indistinguishable on screen. On 2026-08-05 that
ambiguity hid a live incident for hours — 59 of 61 windows blank, no diagnostic
anywhere, three hypotheses eliminated by hand before the cause was found
(E-1898, absorbing E-1895).

The bar deliberately stays silent on stderr: it re-execs once per pane every
`status-interval` (2s across a dozen-plus panes), so logging would be a firehose
painted over a live TUI. Recording a fault is the right shape instead — the
incident dedupes in place, so a bar failing on every pane every two seconds
raises **one** incident with a rising occurrence count.

**Coverage limit, worth knowing.** The fault recorder's database accessor is
`monitor.DB` itself, and `faults.Record` swallows its own failures by contract
(it must never turn a diagnostic problem into a user-visible one). So a
status-line failure *caused by* the database being unreachable cannot be
recorded — the recorder needs the same handle that just failed. This code
therefore covers the schema, enum-integrity, and DB-context gates behind
`monitor.DB()`, not a genuinely unopenable database.

**What to do.** Read the detail (`endless errors show <n> --detail`); it
carries the underlying error and the pane id. An enum-integrity failure means the
binary and the database disagree about an enum mirror; `endless db upgrade`
reseeds it. A schema version mismatch is raised as ERR-0020 instead. If the bar
is blank with *no* incident recorded, suspect the database itself and check
`endless sql "select 1"`.

## WARN-0009 — triage-failed

**Severity:** warning · **Raised by:** nothing — retired by E-1993

Triage could not reach a verdict for a task, so the task was left `untriaged`.
The description triage that raised it, and the `untriaged` status it guarded,
were removed by E-1993: a task needs a plan before it can be claimed or
spawned, so no description is ever judged a sufficient spec. The code stays in
the catalog because a number is never reused, and because incidents recorded
before the removal still carry it.

**What to do.** Nothing to fix: the description triage was removed (E-1993) and
nothing raises this code any more. An incident still carrying it predates the
removal — dismiss it with `endless errors clear <id>`.

## ERR-0010 — worktree-probe-failed

**Severity:** error · **Raised by:** the ◆ unsettled probe (`session status`,
`task unsettled`) and the worktree reaper (E-1940)

A git probe behind the ◆ marker could not run for a task's worktree — either
`git status --porcelain`, or one of the commands that work out which commits'
content has not reached the base branch (`git merge-base`, `git rev-list`,
`git range-diff`, `git log`).

The severity is about what the failure *looked like* before this code existed.
The probe was fail-open: any git error yielded `false`, the settled verdict, and
`Reason()` rendered the words "settled (git status failed: …)". A worktree
nobody could inspect was therefore drawn exactly like a worktree verified clean,
on the one surface whose whole job is answering "is my work safe to walk away
from?". The reaper — running the same two probes — had always failed *closed*,
so the two surfaces that claim to agree on "done and landed" disagreed precisely
where it mattered.

Now the probe fails closed too: an unrunnable probe marks the task's own row ◆
and records this fault. That widens ◆ from "you have work to land" to "look at
this task — unlanded or uncheckable"; `endless task unsettled <id>` tells you
which of the two, naming the failing command and git's own message.

Repeats collapse on (worktree, failing probe), so `session monitor` re-probing
every row every two seconds raises **one** incident with a rising occurrence
count.

**What to do.** Read the detail (`endless errors show <n> --detail`); it
carries the worktree path, the failing git command and its stderr. The usual
causes are a worktree directory whose git administrative file is stale or gone
(`git worktree list` disagrees with the disk) and a base branch that does not
exist locally. Dismiss with `endless errors clear <id>`.

## ERR-0011 — default-branch-unresolved

**Severity:** error · **Raised by:** `monitor.DefaultBranch` via the unsettled
probe and the worktree reaper (E-1940, absorbing E-1166)

Endless could not work out which branch this repository's work lands into. Every
resolution step fell through: no `default_branch` in `.endless/config.json`, no
`refs/remotes/origin/HEAD` (it is unset until `git remote set-head` runs — the
normal state of a fresh clone), no `init.defaultBranch` naming a branch that
exists here, and neither `main` nor `master` present.

Error rather than warning because of the blast radius: the resolver backs both
the ◆ marker and the reaper's settled condition, so when it fails every
worktree in the project becomes unjudgeable at once and no worktree can ever be
reaped. This is the condition that used to be invisible — the probes hardcoded
`main`, so a repo whose default branch is `master` got exit 128 on every tick, a
permanent false all-clear, and a ◆ that could never appear.

Endless will not substitute `main` here. Guessing is the bug this code exists to
report.

**What to do.** Set the branch explicitly — add `"default_branch": "<branch>"`
to the project's `.endless/config.json`, which beats every detection step. Or
give git the answer it is missing: `git remote set-head origin --auto` populates
`origin/HEAD` for a clone that never had it. Dismiss with
`endless errors clear <id>`.

## WARN-0012 — unlanded-cache-unwritable

**Severity:** warning · **Raised by:** the unlanded-verdict cache under the git
common directory (E-2128)

Endless keeps the answer to "does this branch hold work the base branch lacks?"
in a small derived-state cache at `<git-common-dir>/info/endless/unlanded/`, so
that one background job computes it and every display merely reads it. This
incident says that directory cannot be created, or an entry in it cannot be
written.

Warning rather than error, and that is the whole difference from ERR-0010: every
probe still runs, and every answer Endless gives is still exact. What is lost is
the ability to *remember* one. So the ◆ column in `session status` and
`session monitor` shows `~` — not yet determined — for every clean worktree
indefinitely, because a read-only cache is a permanent miss, and
`endless task unsettled <id>` pays the full `git range-diff` comparison on every
invocation instead of once per branch tip. Correct, just not fast.

Fingerprinted on the cache directory rather than on a worktree: one unwritable
directory is one condition with one remedy, so this raises a single incident
however many worktrees the pass covered.

**What to do.** Read the detail (`endless errors show <n> --detail`); it
names the directory and the filesystem error. The usual causes are a checkout on
read-only media, a `.git` directory owned by another user, and a full disk.
`git rev-parse --path-format=absolute --git-common-dir` from inside the
repository prints the parent the cache wants to live under. Nothing needs
repairing afterwards — the cache is rebuildable derived state, and the next job
pass refills it. Dismiss with `endless errors clear <id>`.

## WARN-0013 — turn-failed-transient

**Severity:** warning · **Raised by:** the Claude Code `StopFailure` hook
(E-2145)

A Claude Code turn ended on an API error that should pass on its own —
`rate_limit`, `overloaded`, `server_error`, `max_output_tokens`, or any error
type Endless does not recognise. Claude Code fires `StopFailure` *instead of*
`Stop` when a turn dies this way, so without a hook on it the turn would end
with no record anywhere and the session would keep reading `working` forever.

The incident is what makes the failure visible; the session itself is already
handled. Endless parses the transcript the dead turn produced and moves the
session to `idle`, which is what it is: paused, waiting on a person. There is no
separate session state for a crashed turn — the fault is where the *reason*
lives.

Fingerprinted on the error type, so a session that hits the same rate limit
twenty times raises one incident with an occurrence count of twenty, while a
different failure opens its own. The summary names the type, so the fault row says
which one it was.

Warning rather than error because these clear themselves: waiting out a rate
limit, retrying an overload, or shortening a reply that hit
`max_output_tokens` is all that any of them need. An unrecognised error type
also lands here — an unknown failure is more likely transient than fatal, and
grading it red would let it outrank a real error for the single line the fault
row renders.

**What to do.** Usually nothing but take the turn again. Read the detail
(`endless errors show <n> --detail`) for the session, the task and the
error type of every occurrence. A high occurrence count on `max_output_tokens`
is worth acting on — it means turns are routinely being cut off mid-reply.
Dismiss with `endless errors clear <id>`.

## ERR-0014 — turn-failed-fatal

**Severity:** error · **Raised by:** the Claude Code `StopFailure` hook
(E-2145)

A Claude Code turn ended on an API error that will not clear itself —
`authentication_failed`, `billing_error`, `oauth_org_not_allowed`, or
`account_on_hold`. Every one of these is a fact about the account rather than a
passing condition, so retrying cannot change the answer; the same turn will die
the same way until a person fixes something outside Endless.

Identical to WARN-0013 in everything but severity and remedy: the same hook, the
same transcript parse, the same move to `idle`, the same fingerprint-per-error-
type grouping. Two codes rather than one whose colour depends on the payload,
because severity here is a property of the code — one code that sometimes meant
yellow and sometimes red would be invisible in the catalog and in
`endless errors codes`.

Error rather than warning so it wins the fault row's single line against a
transient failure that happens to be open at the same time. That is what
severity buys here: prominence, not longevity.

**What to do.** Fix the account condition the error type names — re-authenticate
(`claude` will prompt), settle billing, or ask whoever administers the
organisation about an org policy or a hold. `endless errors show <n> --detail`
names the error type, the session and the task for every occurrence.
Dismiss with `endless errors clear <id>` once it is sorted.

## ERR-0015 — hook-write-failed

**Severity:** error · **Raised by:** every Endless Claude hook (E-1887)

A hook could not write the row it exists to write — the `sessions` upsert that
binds a session to its pane, the throttled activity record, the session
lifecycle transitions.

This is the code behind the incident that created all four. Session ES-1055 ran
for roughly four weeks with a blank tmux status bar: every `PreToolUse` died
inside `monitor.TouchSession` with `table sessions has no column named process`,
`sessions.process_id` stayed NULL, and the status line — which resolves a pane
through exactly that column — correctly found no session and rendered the hint a
pane with no Endless context gets. Nothing was recorded anywhere, because hooks
exit without blocking on failure by contract and Claude Code discards hook
stderr.

It is the most consequential of the four hook codes. A write that did not happen
leaves state behind that every later read believes, so the failure is silently
load-bearing rather than merely lost.

**What to do.** Read the detail (`endless errors show <n> --detail`); it names
the hook event, the session, the pane and the binary that ran. A "no such
column" or enum-integrity failure means that binary and the database disagree
about the schema, repaired by bringing the binary up to the database — `just
install` from the main checkout. It names the binary because a worktree used to
pin its hooks at its own `bin/endless-go`, which a schema change left behind;
E-2166 removed that pin, so a hook now runs the installed one. Until it is fixed
the session's `process_id` stays NULL, so the tmux status line renders the same
hint a pane with no Endless session gets.

## ERR-0016 — hook-read-failed

**Severity:** error · **Raised by:** every Endless Claude hook (E-1887)

A hook could not read what it needed before it could do anything: the project
enclosing its working directory, the activity throttle, the active-task query.

Same causes as ERR-0015 and a different consequence, which is why it is a
separate code rather than the same one. The hook returns before it touches
anything, so it did no work for that event rather than leaving a wrong answer
behind, and the next event retries from scratch.

**What to do.** Read the detail (`endless errors show <n> --detail`); it names
the hook event, the session and the binary that ran. The causes are the same
ones behind ERR-0015 — a binary and a database that disagree about the schema —
but the consequence is narrower: the hook did no work for that event rather
than leaving wrong state behind, and the next event retries from scratch.

## ERR-0017 — hook-payload-unreadable

**Severity:** error · **Raised by:** every Endless Claude hook (E-1887)

The harness sent the hook nothing on stdin, or sent bytes that are not the JSON
event envelope every hook is invoked with.

Separated from the two database codes because the remedy has nothing in common
with theirs. No amount of rebuilding a binary fixes a hook whose stdin is being
piped through another command by a `settings.json` entry, and a reader sent to
look for schema drift is being sent the wrong way.

**What to do.** This one is not a database problem. Claude Code either sent the
hook nothing on stdin or sent something that is not the JSON event envelope, so
check how the hook is wired in `settings.json` — an entry that pipes the hook's
input through another command is the usual cause. The detail (`endless errors
show <n> --detail`) carries what was received.

## ERR-0018 — hook-failed

**Severity:** error · **Raised by:** every Endless Claude hook (E-1887)

A hook failed in a way that carries no more specific code.

All four hook codes are recorded at ONE site — the error sink in `hook.Run`,
which every error that ends a hook invocation already passes through — so that a
handler added later is covered without anyone remembering to add a call. The
error is CLASSIFIED where it is raised rather than sniffed out of its text at the
sink, and this code is what an unclassified error gets.

It is the least useful of the four by construction, which is the argument for
keeping the other three rather than collapsing them into it: a report saying
only "a hook failed" has told its reader nothing they can act on. Its detail line
still carries the full error text, so even here the operation that failed is
named. Today the unclassified paths are cwd resolution and worktree adoption.

**What to do.** Read the detail (`endless errors show <n> --detail`): it
carries the full error text, the hook event, the session and the binary that
ran, and the error text names the operation that failed. This code is the one
raised when no more specific hook code applies, so a run of them against a
single operation is a sign that operation has earned a code of its own.

## WARN-0019 — monitor-restart-failed

**Severity:** warning · **Raised by:** `session monitor` and `project monitor`
(E-2193)

A live monitor runs for days, and it keeps running the binary it started with.
So each one watches the file it was launched from: when an install replaces that
file and the replacement has held still for two ticks, the monitor runs the new
binary with `--help` as a probe and then re-execs it in place — same pane, same
process. This incident says that restart did not happen: the probe failed or
timed out, or the exec itself returned an error.

Warning rather than error, because the monitor keeps rendering with the old
binary. What it gives up is background jobs: from that moment until the process
ends it fires none, because a stale binary must never again run a job the new
install may have retired. The frame says so on its last line. Other monitors that
restarted cleanly keep firing jobs as usual.

Fingerprinted on the replacement's resolved path, so one bad install raises one
incident however many monitors tripped over it.

**What to do.** Read the detail (`endless errors show <n> --detail`); it names
the stage that failed — `probe` (the new binary could not even print its usage)
or `exec` — and the error. Fix or rebuild the install; a monitor retries on its
own whenever the binary changes again, so a good build usually clears it without
a restart. A monitor that still shows the notice has stopped firing background
jobs: restart it — `endless session monitor --restart` does every session
monitor in the tmux session at once; a project monitor is quit and started again
by hand. Dismiss with `endless errors clear <id>`.

## ERR-0020 — schema-version-refused

**Severity:** error · **Raised by:** the Claude hook, the tmux status line and
the background job runner (E-2020)

A connect no longer brings the database up to date as a matter of course. It
compares the database's schema version with the newest one the binary carries:
a database BEHIND an installed binary is backed up and migrated forward, a
database AHEAD of any binary is refused, and a binary built inside a task
worktree never opens the main database at all (ED-1601). This incident is one of
those refusals, met by a surface that fires constantly for nobody.

Those surfaces stay silent — the hook exits 0 with no output, the status line
renders its placeholder — because printing an error per event is how the
2026-08-10 land produced fifty identical lines after it had succeeded. The
refusal is recorded here instead, fingerprinted on its summary, which carries the
kind of refusal and both versions and nothing per-event: every hook on the
machine during one land window is one incident with a rising count. An
interactive `endless` command meeting the same refusal prints it.

**What to do.** Read the summary: it names which of three cases this is.
`database is at schema vN, endless-go carries vM` means the binary is older than
the database — upgrade endless (in a self-dev checkout, `just build` in the main
checkout); a self-dev land clears the one its own migration causes, so one that
stays open is real. `migrating the database ... did not complete` means a forward migration failed —
run `endless db upgrade`, which backs up first. `a worktree-built endless-go
refused the main database` means something ran a worktree's binary against main;
use the installed binary. Dismiss with `endless errors clear <id>` once the cause
is gone.

## WARN-0021 — output-style-inactive

**Severity:** warning · **Raised by:** `endless register` and `endless-go outputstyle install` (E-2159)

The output style file is in place, so every surface reports it as installed,
while the style itself is doing nothing at all. Activation is the user's opt-in
by design — installing never activates — so this fires on a perfectly
successful install.

It is recorded rather than printed because the only reader who can act on it is
the user: `/config output-style` is a slash command no agent can run, and
`--activate` is right only when the user asked for the style to be in effect.
Printed to stderr it cost the agent a message it could do nothing with.

**What to do.** Activate it when you want it in effect — `/config output-style=endless` in a Claude session, or re-run the install with `--activate`. Leave it inactive and dismiss this with `endless errors clear <id>` if you installed the style without meaning to switch to it.

## WARN-0022 — worktree-ttl-unreadable

**Severity:** warning · **Raised by:** the stale-worktree sweep (E-2159)

`worktree_ttl` in the project's `.endless/config.json` did not parse, so the
sweep ran on the built-in default instead. Nothing is blocked and no worktree is
treated differently than it would have been under the default.

It is the project's own configuration file, which no retry of any command
changes and no agent should be editing on its own — which is why it is recorded
for the user rather than printed at whoever happened to trigger the sweep.

**What to do.** Fix `worktree_ttl` in the project's `.endless/config.json` — it takes a Go duration (`336h`) or a day count (`14d`). Until then every sweep uses the default, which is the same answer it gave before the value was added. Dismiss with `endless errors clear <id>` once the value parses.

## WARN-0023 — unsupported-harness

**Severity:** warning · **Raised by:** the `endless` CLI's root group (E-2006, E-2159)

A command ran under a harness Endless does not support — Claude Code Desktop,
or anything no detector claims. Endless supports Claude Code in the terminal
only: its hooks do not fire the way the guide assumes, so session tracking, task
claiming and worktree routing cannot work as documented.

The agent is still told, in its own refusal, to stop invoking Endless for the
rest of the session — that part is not a warning and does not move. What is
recorded here is the fact for the USER, who is the only one who can change which
harness they are running in, and who would otherwise learn it only from an
agent's aside.

**What to do.** Run Endless from Claude Code in a terminal if you want it to work as documented; its hooks do not fire on other harnesses. Nothing is wrong with the install. Dismiss with `endless errors clear <id>` — the agent has already been told to ignore Endless for that session.

## WARN-0024 — report-unminimized

**Severity:** warning · **Raised by:** `endless task report` (E-2159)

The minimizer broke a protected-content invariant twice on one reply, so
Endless sent the agent's draft through unminimized rather than a reply with
mangled content. Nothing was lost: the draft is intact and the reply went out.

Before this code the fallback was announced only to the agent, which under the
report rule does not relay a no-report line — so "announced, never silently"
had quietly become "announced to nobody who could act on it". The minimizer
misbehaving on real drafts is exactly the signal its owner needs, and it belongs
where the user will see it accumulate.

**What to do.** Nothing, for the reply — it was sent, and intact. The signal is the minimizer itself: read the detail (`endless errors show <n> --detail`) for which invariant broke, and treat a rising occurrence count as a bug in the minimizer rather than in the drafts. Dismiss with `endless errors clear <id>`.

## WARN-0025 — sigil-synonym

**Severity:** warning · **Raised by:** the `UserPromptSubmit` hook's sigil recorder (E-2159)

A sigil used for the first time is within a character or two of one already in
the corpus — `$fastpath` beside `$fast-path`. Either both are deliberate and
both keep working, or one is a typo that will quietly split one label's data
across two names.

Only the user can say which, and the question is not urgent: nothing is blocked
and both spellings keep recording. Asked through the agent it cost a turn's
attention on every prompt that used the new sigil; recorded here it waits until
the user is deciding about their own vocabulary.

**What to do.** Decide whether the two spellings mean the same thing. If they do, pick the one you want as canonical and use it from then on — the other keeps working, so nothing breaks while you switch. If they are genuinely different labels, nothing needs doing. Dismiss with `endless errors clear <id>` either way.

## WARN-0026 — create-hook-not-executable

**Severity:** warning · **Raised by:** worktree creation — `endless task claim`, `endless task spawn` (E-2213)

The project ships a `.endless/hooks/post-worktree-create.sh` bootstrap, but the
file is not executable, so a new worktree was created without running it. The
worktree itself is fine; whatever the hook installs or builds is not there yet.

The warning still prints for whoever ran the command. It is recorded here too
because the fix is a mode change on a file tracked in the main checkout, which
is the user's to make and commit — an agent working in one worktree is told only
how to finish that worktree's bootstrap. Fingerprinted on the hook, so every
worktree created before it is fixed is one incident.

**What to do.** Make the hook executable on main — `chmod +x .endless/hooks/post-worktree-create.sh`, then commit the mode change — so later worktrees run it. A worktree created while it was skipped is usable but not bootstrapped; finish it with `sh .endless/hooks/post-worktree-create.sh <worktree>`. Dismiss with `endless errors clear <id>` once the hook runs.

## WARN-0027 — post-land-not-executable

**Severity:** warning · **Raised by:** `endless worktree land` (E-2213)

A task committed a one-time `.endless/hooks/post-land/<task>.sh` to run after it
landed, but the file is not executable, so it was skipped. The land itself
succeeded and is not undone; the step the script carries — typically removing
files a newly un-ignored path left behind — has not happened.

It is recorded here as well as printed because the land is often driven by an
agent whose reply scrolls away, and a skipped one-time step is easy to lose
there and hard to notice later.

**What to do.** Run the skipped step yourself from the main checkout — `sh .endless/hooks/post-land/<task>.sh <main checkout>`; the script is required to be idempotent, so running it late is safe, and the incident's summary names the script. Commit future post-land scripts executable (`git update-index --chmod=+x <script>`). Dismiss with `endless errors clear <id>` once it has run.

## WARN-0028 — stale-companion

**Severity:** warning · **Raised by:** commands that read a worktree's companion — `endless worktree list`, `endless worktree current`, `endless task handoff` (E-1301, E-2213)

A worktree's `.endless/worktree.json` still carries the legacy `task_id` key,
and it names a different task than the worktree's directory does. The directory
name is authoritative, so every command answers correctly; the companion is
simply wrong, and stays wrong until someone edits it.

Printed, the warning reached only whoever ran that one command, and it repeated
on every command that read the companion. Recorded here it is one incident per
worktree, waiting for the person who owns the file.

**What to do.** Remove the legacy `task_id` key from the worktree's `.endless/worktree.json` (the incident's summary names it); nothing reads it any more, and the path-derived task is already the one in use. Dismiss with `endless errors clear <id>` once the key is gone.
## WARN-0029 — rate-failed

**Severity:** warning · **Raised by:** the `rater` job and `endless rater run`
(E-2203)

The rater proposes complexity and risk for a `submitted` task that nobody rated
— in practice one a person filed, since an agent cannot promote a task to
`submitted` without rating it. This incident says one task got no proposal: the
model call timed out, `claude` was not found or exited non-zero, its reply
carried no usable `COMPLEXITY:` or `RISK:` line, or reading the task or
rendering the prompt failed first.

Warning rather than error, because the rater is fail-open: the task keeps its
status and stays unrated, nothing is written, and the next run retries it.
Fingerprinted on the cause rather than the task, so a machine with no `claude`
raises one incident however many tasks are waiting.

**What to do.** Check that `claude` is on PATH and answering — the incident's
detail carries the failing step. Nothing is lost: the task keeps its status and
the rater job retries it on its next run. To rate it now, run `endless task
update <id> --complexity <low|medium|high> --risk <low|medium|high>`, or
`endless rater run --task <id>` to retry the model. Dismiss with `endless errors
clear <id>`.

## ERR-0030 — main-diverged

**Severity:** error · **Raised by:** the `main-sync` job (E-2233)

A project opted into `main_sync`, and its default branch and that branch's
upstream each hold commits the other lacks. A fast-forward would lose main's
commits and a push would be rejected, so the job changed nothing and stopped
syncing. It never chooses between merge and rebase for you, and it never runs
`git pull`, so your `pull.rebase` setting cannot choose for it either.

Fingerprinted on the project's branch: a divergence that lasts across runs is
one incident with a rising occurrence count.

**What to do.** Decide how to bring the two together; the job changes nothing until you do. Merging (`git merge <upstream>` on main) costs one merge commit and nothing else, because nothing in Endless assumes main is linear. Rebasing (`git rebase <upstream>` on main) gives every unpushed commit on main a new SHA, so every open task branch must then run `git rebase main` before it can land. The incident's summary names the upstream and how many commits each side holds. The next run pushes once main contains its upstream. Dismiss with `endless errors clear <id>` after that.

## WARN-0031 — main-rewritten

**Severity:** warning · **Raised by:** the `main-sync` job (E-2233)

Main was rebased or amended after some open task branches forked from it. Those
branches still carry the old copies of main's commits, and `endless worktree
land` refuses each of them until it is rebased (E-2232). Nothing has failed
yet: this is raised so the rebase happens before a land is refused.

The job looks for it when main's tip stops descending from the tip it last saw,
and on its first run in each process. A branch with no work of its own is not
listed. Fingerprinted on the project, so one rewrite is one incident however
many branches it strands.

**Reading it.** The summary names up to four branches; when there are more it
says how many and points at `endless errors show <id> --detail`, which lists
them all. For each branch the detail gives the task's status and whether its
claiming session is live or ended, the date by which main rewrote the commits
it duplicates, and the date of the commit it forked from. When every rewrite
predates the job's previous successful run — or the job has never succeeded
before — the summary says so, because a job that has just started watching
reports an old rewrite exactly as it would a new one.

Only open tasks' branches raise it. A finished task's branch (confirmed,
assumed, completed, superseded, declined, obsolete) in the same state is a
retained leftover with nothing waiting on it, so it is named only in the job's
run note (`endless jobs list`).

**What to do.** Run `git rebase main` in each worktree the incident names; git drops the copies main already holds and keeps each branch's own work. `endless worktree check`, run inside a worktree, says the same for that one branch. Do not rename or renumber any migration for this. There is nothing to clear: while branches stay stranded the job re-checks every run and the incident names only those still stranded, and once none are left it clears itself.

## WARN-0032 — job-unreachable

**Severity:** warning · **Raised by:** the background job runner, for a job
that marks its failures transient — today the `main-sync` job (E-2260)

A job failed the same likely-transient way — a DNS failure, a refused
connection, an unreachable network, a timeout — four runs in a row. The runner
records nothing for the first three: a brief outage passes on its own, the job
backs off and retries meanwhile, and `endless jobs list` shows the failure
count and the last error. A failure the job does not mark transient — a
rejected push, a failed authentication, a missing upstream — is still WARN-0001
on its first occurrence.

Fingerprinted on the job, so an outage that lasts is one incident with a
rising occurrence count.

**What to do.** Check the network, any VPN, and the remote's credentials; the summary names the job, how many times in a row it has failed, and the first line of the cause. The job keeps retrying on its own and this warning clears itself on its next successful run. Once the cause is fixed, run `endless jobs retry <name>` to retry now rather than waiting out the backoff.
