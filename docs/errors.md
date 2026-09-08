# Endless error codes

Every classified fault Endless records carries a stable `ERR-NNNN` code. This
page is the catalog: what each code means, why it fires, and what to do about it.

Codes are flat and unprefixed on purpose. A subsystem prefix (`JOB-`, `HOOK-`,
`DB-`) would squat on identifier namespace that project-scoped task IDs may want
— task IDs are `E-NNNN` today and may become per-project prefixes later. Which
subsystem raised a fault is already recorded in its **source** field.

Numbers are never reused. Retiring a code spends its number permanently.

## Seeing and clearing errors

```sh
endless errors show              # open incidents in the project you are in
endless errors show --all        # include cleared ones (history)
endless errors show --id 12 --detail   # one incident, with every logged occurrence
endless errors clear             # mark every open incident cleared
endless errors clear 12 13       # clear specific incidents
endless errors codes             # print this catalog from the running binary
```

`session status` and `session monitor` show a trailing badge whenever open
incidents exist — max severity wins, `error` outranks `warning`.

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
endless errors show                      # this project (plus the machine's own)
endless errors show --project acme       # another project
endless errors show --all-projects       # everything, with a PROJECT column
endless errors clear --all-projects      # dismiss every open incident, everywhere
```

Both flags work the same on `clear`, and they matter there: with no ids, `clear`
dismisses exactly the set a `show` under the same flags would list. Naming ids
overrides the scope — `errors clear 12` clears incident 12 whichever project it
belongs to, because you named it.

**Every scope also includes the errors that belong to no project.** Some failures
are the machine's, not a project's: the background job runner unable to open the
database, the tmux status bar unable to resolve a pane. Those have no project to
be filed under, so they ride along with whichever project you ask about — they
would otherwise be visible on no default view at all. In an `--all-projects`
listing their PROJECT column reads `—`.

Run outside any registered project and there is nothing to scope to, so both
verbs cover the whole machine. The PROJECT column appearing is how a listing
tells you it widened.

The same rule scopes the badge: `project status` and `project monitor` count
their own project's open incidents plus the unattributed ones, while
`session status` and `session monitor` stay machine-wide — they render every live
session on the box, whatever project each is in.

## Where the detail lives

The `errors` table holds only the index — project, code, source, summary, counts.
Each occurrence's full capture (stack traces, command output, the job's own log
output) is appended to `<config-dir>/log/errors.jsonl` and read back by
`errors show --detail`. The table therefore stays bounded by the number of
*distinct* faults rather than by how often they happen, and nothing is lost to
diagnosis.

Detail lines carry the project by NAME (`"project": "acme"`), because that file
is read without a database — one log holds every project on the machine. Lines
written before projects were recorded simply have no `project` key; nothing
rewrites them, since a fault's project cannot be reconstructed after the fact.

That file is machine-local. It is not the shareable db-ledger, it is never
replayed into the database, and faults emit no ledger events.

---

## ERR-0001 — job-failed

**Severity:** warning · **Raised by:** the background job runner

A registered background job's `Run` returned an error.

This is a warning rather than an error because the runner recovers: it records
the failure, releases the lease, and reschedules the job. One failure is not yet
a broken system.

**What to do.** Read the detail (`endless errors show --id <n> --detail`) — it
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

## ERR-0004 — job-scheduling

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
current. A binary pinned onto a database it does not own opens schema-passive
(E-1818) and will not have created the `jobs` table; that is the expected cause
if you are running a worktree build against the main database.

## ERR-0005 — job-stuck-lease

**Severity:** warning · **Raised by:** the background job runner

A job finished, but by then its lease had already been re-claimed by another
invocation — so two invocations may have run it concurrently.

**What to do.** Raise the job's `Schedule.LeaseTTL` above its realistic
worst-case runtime. Also confirm the job is genuinely idempotent: the lease is
time-boxed rather than an OS lock precisely so a dead process needs no cleanup,
and the unavoidable cost of that design is that a slow job can be re-entered.

## ERR-0006 — test-warning

**Severity:** warning · **Raised by:** `endless errors raise`

Nothing is wrong. This code exists only so the error surface can be exercised on
demand — the badge on `session status`, the `errors show` listing, the JSONL
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
`session status` badge, which reads main.

**What to do.** Dismiss it: `endless errors clear <id>`. If you did not raise it
yourself, someone was testing; it is not a fault report.

## ERR-0007 — test-error

**Severity:** error · **Raised by:** `endless errors raise --severity error`

The error-severity counterpart to ERR-0006, for exercising the surfaces that
treat `error` differently from `warning` — the red badge styling, the max-severity
precedence, and the rule that an error never ages off the badge while a stale
warning does.

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

**What to do.** Read the detail (`endless errors show --id <n> --detail`); it
carries the underlying error and the pane id. A schema or enum-integrity failure
means the binary and the database disagree — usually a worktree build against
the main DB (E-1818). If the bar is blank with *no* incident recorded, suspect
the database itself and check `endless sql "select 1"`.

## ERR-0009 — triage-failed

**Severity:** warning · **Raised by:** `endless triage run` (E-1859)

Triage could not reach a verdict for a task, so the task was left `untriaged`.
Causes: the model call timed out, `claude` was not found on PATH, the process
exited non-zero, or the reply did not begin with `SUBMITTED:` or `UNPLANNED:`.

Nothing is lost or corrupted — triage is fail-open, the task keeps its
`untriaged` status, and the background sweep retries it. The warning exists
because the file-time triage path runs DETACHED: without it, a child that
crashed and a child that considered the description and declined to route look
identical, and neither is recorded anywhere.

The source names which path failed — `triage:inline` for the child `task add`
spawns, `triage:sweep` for the background job. Repeats collapse into a single
incident with an occurrence count, so a machine with no `claude` installed
raises one warning rather than one per filing.

**What to do.** Check that `claude` is on PATH and answering — the incident's
detail log carries the failing invocation. If triage is not wanted on this
machine, set `ENDLESS_NO_TRIAGE=1` to stop the automatic path, or route by hand
with `endless task submit <id>` / `endless task update <id> --status unplanned`.
Dismiss with `endless errors clear <id>`.

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

**What to do.** Read the detail (`endless errors show --id <n> --detail`); it
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
