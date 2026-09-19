# E-1940 — Make the unsettled probe stop reporting "I could not tell" as "you are clear"

Absorbs E-1166. Decisions below are settled unless flagged OPEN.

## Problem

`UnsettledDetail.Unsettled()` returns false — the all-clear — whenever its git
probes could not run. The reaper runs the SAME two probes and already fails
closed ("Any git error while running 4 or 5 is treated as 'in use' — the reaper
would rather skip a candidate it can't reason about than destroy in-flight
work"). So the two surfaces the code claims agree on what "done and landed"
means actually diverge exactly where it matters, and the display picked the
unsafe polarity.

The hardcoded `main` is what makes this permanent rather than rare: on a repo
whose default branch differs, `git rev-list main..HEAD` exits 128 every time,
so `◆` can never appear and the reaper never reaps.

## Settled decisions

| # | Decision | Rejected alternative |
|---|----------|----------------------|
| 1 | Probe failure records a **fault** (E-698's system, already landed). | A new `?` glyph and legend entry. |
| 2 | Default branch resolved by one **shared helper**, used by the unsettled probe AND the reaper. | Fixing the display only and leaving the reaper on `main`. |
| 3 | E-1166 is **absorbed**: the Python `_default_base_branch` fix lands here too, under one strategy. | Two independent resolvers, one per language. |
| 4 | A probe failure marks the task's own row `◆`, alongside the fault. | Badge-only: `◆` keeps its narrow meaning and the row renders as if clean. |

## A. Shared default-branch resolver

New `monitor.DefaultBranch(repoDir) (string, error)`. Resolution order:

1. `.endless/config.json` `default_branch`, when set — explicit beats detection.
2. `git symbolic-ref --short refs/remotes/origin/HEAD`, stripped of `origin/`.
3. `git config init.defaultBranch`.
4. Probe `main`, then `master`, for an existing local ref.
5. Otherwise a real error — the caller decides, and no caller may silently
   substitute `main`.

Step 2 is unset on fresh clones (it needs `git remote set-head`) — that is
E-1166's original finding and why steps 3–5 exist. Resolution is per-repo and
should be memoized for the process; `session monitor` calls the probe every 2s
per row and must not add a git call per row per tick.

## B. Call sites

- `internal/monitor/worktree_unsettled.go` — `worktreeUnsettledAt`'s probe 2.
- `internal/monitor/reap_worktrees.go` — `maybeReapWorktree` condition 4.
- `src/endless/worktree_cmd.py` — `_default_base_branch` (absorbed E-1166).

The Go and Python implementations must agree on the order above; assert that in
the tests rather than trusting prose.

## C. Polarity and reporting

`UnsettledDetail` gains an explicit undetermined state rather than overloading
the existing booleans:

- `Unsettled()` returns **true** when a probe failed, so the task's own row is
  marked. The alternative — report only in the badge and leave the row looking
  clean — was rejected: the row is the surface actually scanned, and a row that
  renders identically to a verified-clean one is the exact failure this task
  exists to remove. Marking the row is what tells you WHICH task is
  unverifiable without opening the fault.
- This widens `◆` from "you have work to land" to "look at this task — unlanded
  or uncheckable". That is a deliberate trade, and it is why the distinction
  below must be retrievable.
- `IsUndetermined()` reports it distinctly, so `task unsettled <id>` says which
  probe failed and why. `Reason()` already prints the error text today — that
  half is honest and stays. The list view collapses; the detail view explains.
- No new glyph, and no legend change beyond the fault badge E-698 already
  renders.

A fault is recorded on probe failure, so the condition is visible even when no
one is looking at that row, and is clearable. Faults are deduped by
fingerprint — key on (task id, failing probe) so a monitor ticking every 2s
raises one incident, not thousands.

## D. Fault codes

Two new entries in `internal/faults/codes.go` (numbers never reused; take the
next free ids):

- **worktree-probe-failed** — `git status` or `rev-list` failed for a worktree.
  Severity `error`: the user asked "is my work safe?" and Endless could not
  answer.
- **default-branch-unresolved** — resolution fell through every step in A.
  Severity `error`, same reasoning; it disables the probe entirely.

Both carry the task id, worktree path, failing command, and git's stderr in
`Fields`/`Detail`.

## E. Reaper

Condition 4 takes the resolver. Its fail-closed handling is already correct and
must not be touched. Fixing the branch resolution is what lets a non-`main`
project reap at all — today every candidate errors and is skipped forever.

This change touches the code path that DELETES worktrees, so the tests must pin
that a resolver error still yields "skip", never "reap".

## F. Landed state comes from the recorded landing

Scope confirmed with Mike 2026-08-25: this task ships the analysis's half too,
because it lands in the same two call sites as A-E and splitting it would mean
one rewriting the other.

`worktree land` rebases before fast-forwarding, so a landed commit is rewritten
under a new SHA while the branch keeps the original. `<base>..HEAD` therefore
reports a correctly-landed branch as unlanded forever, the count growing as the
base advances, and the remedy it prints would replay stale commits. The same
probe is reap condition 4, which is why landed worktrees accumulate.

One primitive, three consumers:

- unlanded = commits reachable from HEAD, reachable neither from the base
  branch nor from any recorded landing
  (`rev-list --count --ignore-missing HEAD ^<base> ^<landing>...`).
- The unsettled probe and the reaper both build their range through it, so
  they cannot disagree about what "landed" means.
- `worktree land` on a task whose worktree is gone consults the recorded
  landing and reports "already landed (commit <sha>)" instead of implying the
  work is lost. Absorbs E-1308.

The credit anchors on the BRANCH, not on the base: a recorded sha is excluded
from the branch's own history. Reachability from the base was the analysis's
original proposal and was falsified by measurement before implementation — a
rewrite of the base detaches every landing recorded before it, including every
one belonging to the case the analysis was written from. The analysis records
the revision and the evidence.

The path-based probe resolves landings best-effort from the `e-NNNN` directory
name. That softens E-1766's DB-free rule rather than reversing it: the lookup is
never authoritative, every failure is silent, and a miss only returns the count
to its pre-E-1940 over-reporting. Python threads its resolved `--config-dir` to
the Go subprocess so `--db main` renders a verdict computed against the same
database the caller is reading.

## Verification

**Durable tests** (these outlive the task):

- Go: resolver returns each step's answer in order, and errors rather than
  defaulting to `main` when all fail; `Unsettled()` is true on `StatusErr` and
  on `RevListErr`; `IsUndetermined()` separates that from real unsettled work;
  a fault is recorded once per (task, probe) rather than per tick; the reaper
  skips on resolver error.
- pytest: the Python resolver agrees with the Go order.
- A `master`-based fixture repo: `◆` appears for genuinely unlanded work and
  the reaper can reap. This is the regression that proves the product works
  outside `main`.
- The landing credit: a rebase-landed branch reads settled; a commit made AFTER
  the landing still reads unlanded; and rewriting the base's history afterwards
  does not move either verdict.

**`tests/tasks/e-1940-verify.sh`** — acceptance. **Author a script, NOT a
`.endless/tasks/e-1940/verify.toml`**: the manifest form is not the norm until
the verification-suite epic lands.

## Absorbed

- **E-1166** — absorbed; mark obsolete with an outcome naming this task.

## Not in scope

Local-vs-remote divergence (unpushed commits, `origin/main` ahead/behind).
`session status` answers "is this worktree's work landed into main?" — landing
is a local rebase + fast-forward, so LOCAL `main` is the correct reference and
the current comparison is right.


