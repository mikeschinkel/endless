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
endless errors show              # open incidents
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

## Where the detail lives

The `errors` table holds only the index — code, source, summary, counts. Each
occurrence's full capture (stack traces, command output, the job's own log
output) is appended to `<config-dir>/log/errors.jsonl` and read back by
`errors show --detail`. The table therefore stays bounded by the number of
*distinct* faults rather than by how often they happen, and nothing is lost to
diagnosis.

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
