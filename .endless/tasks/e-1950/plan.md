# E-1950 — session-status fault badge

Three reported defects, discussed with Mike before implementation. Decisions he
made are marked **[Mike]**.

## Defect 1 — a transient fault pinned to the badge with no way to resolve it

The incident was a single `SQLITE_BUSY` while creating the `triage-sufficiency`
job scheduling row, one occurrence, self-healed, still badged a day later.

Two independent causes:

**1a. It should never have been a fault.** `runOne` raised ERR-0004 on any
`ensureRow` failure, while `docs/errors.md` already said of that code: "no job
state is corrupted ... the next invocation retries. A persistent occurrence
means something is wrong with the database itself." Losing a single lock race is
what a database with concurrent writers does.

*Fix:* `ensureRowWithRetry` — three attempts, each behind the connection's
existing `PRAGMA busy_timeout=5000`, 250ms apart. `isBusy` matches on message
text, so a missing table or disk error still faults immediately without
retrying. **[Mike: ok]**

**1b. The dismissal was undiscoverable.** The badge named only
`endless errors show`, and `errors show` never mentioned `errors clear`. Mike
read the dim hint line as an oversight rather than an instruction.

*Fix:* `printClearHint` closes `errors show` with both forms of `errors clear`
plus the clear-is-not-retry distinction. Suppressed when nothing open.

**1c. Stale warnings age off the badge.** The E-698 design comment said clearing
is manual "so an intermittent fault cannot heal itself out of view". That rule is
right for a fault that is still happening and wrong for one that fired once and
healed. **[Mike: agreed — the existing comment contradicts his original
intention.]**

*Fix:* a warning stops being badged once **one hour of active time** has passed
since its last occurrence. Errors never age off. The incident is not cleared or
deleted — `errors show` still lists it.

**[Mike: the hour must not elapse while he is away from the computer.]** So the
clock is `monitor.ActiveSecondsSince`, which sums only the gaps between
consecutive `activity` rows shorter than `IdleGap` (5 min), plus the open segment
up to now. Idle stretches contribute nothing, so a warning cannot expire
overnight unseen. Not scoped to a project — "was the user at the machine" is not
a per-repo question.

*Placement:* the policy lives in `sessionstatuscmd`, not in `faults`. The
`faults` package deliberately imports nothing from the rest of Endless, and its
storage contract ("nothing is hidden or deleted") is unchanged — the badge simply
decides what is worth a row. `faults.Summarize` was split out of `Open` so the
filtered set produces counts consistent with what is displayed. `badgeworthy`
takes the clock as a parameter so the policy is testable without a DB.

*Failure direction:* an unreadable activity table or an unparseable timestamp
keeps the incident. Never hide what cannot be justified hiding.

## Defect 2 — the hint burned a whole screen row

`renderFaultBadge` emitted a second `Fprintln` containing only a dim
`        endless errors show`.

*Fix:* one row — chip, incident text, hint right-aligned. The hint is reserved
out of the width budget *before* the text, so it survives truncation; on a
terminal too narrow for both, the text wins and the hint is dropped rather than
wrapping.

**[Mike: `See: endless errors --help` is too long — add a shell helper `eeh` and
render it in a different background.]** So the hint is `Run eeh`, and
`eeh` joins `esu`/`esp`/`esf`/`esm` in `endless shell-init` as a wrapper for
`errors show`. The "different background" falls out of the chip inversion below.

The row fills `cols-1`, not `cols`: a line ending exactly at the right margin
sits on the deferred-wrap boundary where tmux panes can emit a spurious second
row, and the monitor sizes its pane from `frameLines`, which counts newlines and
would not see it.

**[Mike: "WARNING 1 warning" is redundant.]** Agreed — `badgeCounts` now returns
"" when there is a single incident, since the chip already says the severity. The
tally appears only when `Total >= 2`, where it says something the one chip cannot.

## Defect 3 — the WARNING chip was an unreadable orange block

Root cause: `\033[30;43m` uses the 30-47 ANSI range, which the terminal **theme
remaps**. Mike's theme renders `43` as a dark orange; black on it is invisible.
The existing code comment shows this was already tuned once in the opposite
direction for a different theme — chasing the palette cannot win.

*Fix:* fixed 256-color indices, which themes do not remap.

| | row | chip (inverts the row) |
|---|---|---|
| warning | `48;5;220` / `38;5;16` — black on gold | `48;5;16` / `38;5;220` |
| error | `48;5;160` / `38;5;231` — white on red | `48;5;231` / `38;5;160` |

**[Mike: invert the entire row — yes.]** The whole row carries the reversed
background so the badge reads as a bar; the chip inverts the row's own pair,
which delineates it without a third color that would have to be legible against
both.

## Files

| File | Change |
|---|---|
| `internal/sessionstatuscmd/faultbadge.go` | rewritten: one row, 256-color inverse, stale-warning filter |
| `internal/sessionstatuscmd/session_status.go` | `frameLines` comment (badge is 1 line now, not 2) |
| `internal/monitor/activity.go` | `ActiveSecondsSince`, `IdleGap` |
| `internal/faults/read.go` | `Summarize` split out of `Open` |
| `internal/jobs/run.go` | `ensureRowWithRetry`, `isBusy` |
| `internal/errorscmd/errors.go` | `printClearHint` |
| `src/endless/cli.py` | `eeh` shell helper |
| `docs/errors.md`, `docs/guide/reference.md`, `docs/guide/orchestration.md` | retry, age-off, `eeh` |

## Verification

`esu && ./tests/tasks/e-1950-verify.sh` — 8 sections, fail-fast on the Go unit
tests, then per-defect assertions, a live render, and the docs gates.

Confirmed against Mike's real data: incident 1 (last seen 2026-08-10T09:49:09,
235 activity rows since) no longer renders on the badge, and is still open and
listed in `errors show`.
