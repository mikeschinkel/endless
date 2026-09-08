# Replace the land's guessed rebase recoveries with proven classification

## Context

`endless worktree land` fails on a rebase conflict by printing two recovery
options as *candidates for the user to choose between*:

1. resolve the conflict in place, `git rebase --continue`
2. capture the delta, `git reset --hard main`, re-apply, re-commit

Both can be actively harmful. Landing E-1943 on 2026-08-12 hit a conflict whose
branch side referenced `BINARY_SOURCE_PATHS` and `_refuse_if_behind_base`,
identifiers E-1941 had just deleted from main. Either recovery reintroduces them
into a function whose global no longer exists, so the land would have
"succeeded" while shipping an `endless worktree sync` that raises `NameError` on
first use. **A recovery that can silently ship broken code must never be
offered.**

Mike caught it because he knows the codebase and does not trust the message. A
product user has neither advantage: they will read two numbered steps as
instructions from the tool and follow one. That is who this is for.

The current shape was itself a deliberate correction — `_rebase_conflict_message`'s
docstring says it offers candidates "never one confident prescription… the
confident misattribution is exactly the failure mode this task removes." That
fixed real misattribution and introduced this hazard. The way out is not a third
rewording: stop speculating and go get evidence.

**The governing fork, resolved:** classification is deterministic even though
resolution is not. The tool can prove *which kind* of conflict this is; only
genuine semantic overlap needs human judgment, and in that one case offering
anything is exactly the mistake.

## Decided

Every question below was settled with Mike before this task was finalized. Do
not re-litigate them; if one turns out to be wrong, reopen and say so.

- **Command:** `endless worktree diagnose [E-NNNN]` — in the worktree noun-group
  beside `land`/`drop`/`check`/`list`, deriving the task ID exactly as `land`
  does so a bare call works in-session.
- **Unproven case: evidence only, no suggestions.** When the class cannot be
  proven, print the evidence and state that a human or agent must choose. Do not
  offer candidates, not even labelled as unverified.
- **Detector reuse: shell out via `endless-go`.** ED-1553's content-based
  fork-point test landed in Go (`internal/events/commit.go`, E-1955). Add an
  `endless-go` subcommand and call it the way `event_bridge` already shells out.
  Do NOT port it to Python — two implementations of one rule silently diverge,
  which is exactly what made the behind-base bug need fixing twice.
- **Sequencing: one atomic land.** Deliverable 1 alone would remove the hazard
  sooner but leave the message pointing at a command that does not exist. Ship
  the whole thing together instead.
- **This is not urgent, and the reason matters.** The only operator today is
  Mike, who knows not to follow those instructions — so nothing is actually at
  risk while this waits on E-1958. The deliverable exists for PRODUCT users, who
  will read the message as authoritative and have no way to know that following
  it can ship a `NameError`. Prioritize accordingly: correctness and clarity for
  a first-time reader beat speed to main.
- **Storage: DEFERRED — blocked by E-1958.** See below.
- **Retention/purge: out of scope.** Neither the `errors` table nor the JSONL
  detail log has any sweep today. Deliberately left for when it is needed.

## Storage is deferred to E-1958 — do not choose it here

The obvious home was the existing fault surface (`internal/faults`): a catalog
`Code` plus `Source` discriminates land conflicts from other uses, and
`Fault.Detail` / `Fault.Fields` already carry long structured capture to a JSONL
log by design, never the DB. That would need no schema change.

It was rejected for now because **the whole fault surface is project-blind**:

- the `errors` table has no `project_id` column
- the JSONL `Detail` struct has no project field
- `monitor.ConfigDir()` under a land resolves to `~/.config/endless` (the land
  pins `--db main`, which sets `dbContextDir` to the real config dir), so the
  log is ONE shared location for every project

Attributing a land conflict would mean smuggling task/project through `Fields`
and the fingerprint — a workaround for a missing column.

**E-1958** ("Make per-worktree sandboxes available to all projects, not just
self_dev") may make per-worktree storage the obvious home instead. Take the
storage location from E-1958's outcome. Everything else in this task is
specified and can be designed against a storage interface in the meantime.

## Deliverables

### 1. `_rebase_conflict_message` stops prescribing

Keep the facts it already reports well — phase, the commit that failed to replay
(`REBASE_HEAD`), the conflicting paths. Delete the two numbered candidates. End
with one instruction: run `endless worktree diagnose`.

Keep the one existing confident path: when every conflicting path is an
endless-managed auto-file, the mechanical recovery is already proven and stays.

Both call sites are affected — Step 3.7 (orphan replay) and Step 4 (base rebase).

### 2. Capture the conflict evidence before aborting

The message is built mid-rebase and then `git rebase --abort` runs, so
`REBASE_HEAD` and the unmerged set are gone by the time anyone diagnoses. Capture
first: phase, base branch, branch, `REBASE_HEAD` sha + subject, unmerged paths,
both sides of each conflicting hunk, `git cherry <base> HEAD` output, and the
base tip sha at failure time. Uncapped — do not truncate hunks.

Write it behind a narrow interface so the destination is a one-line change once
E-1958 resolves.

### 3. `endless worktree diagnose [E-NNNN]`

Human-readable by default, `--json` for agent consumption (matches the rest of
the CLI). Reads the captured evidence; if none exists, says so and exits non-zero
rather than reproducing anything.

**Contract: prescribe only what is proven. Otherwise print evidence and stop.**

| Class | Test | Output |
|---|---|---|
| Already-landed content | `git cherry <base> HEAD` marks `REBASE_HEAD` `-` | Prescribe reset-and-reapply; state that in-place resolve provably duplicates |
| Auto-file-only | every unmerged path satisfies `_is_auto_file` | Prescribe the existing mechanical recovery |
| Orphaned ledger base | content-based fork-point test via `endless-go` (ED-1553) | `_drop_orphan_amendable_commits` covers contiguous-at-base; report when the run is mid-branch |
| Symbol supersession | identifiers in the branch side of each hunk with no definition on base | Prescribe NOTHING. State that both recoveries would reintroduce the missing symbols, name them, and say the branch's work is superseded |
| Semantic overlap | none of the above | Prescribe NOTHING. Show both sides, name the overlapping symbols, state that a human or agent must choose |

### 4. `worktree land --dry-run` rehearses the rebase

Today `--dry-run` only prints worktree, branch, base and main paths — it cannot
preview the failure it exists to preview. Make it rehearse the rebase on a
throwaway branch off the task branch, report the same classification the
diagnostic does, and delete the branch in a `finally`. Never touches base, the
task branch, or the DB.

Same machinery as deliverables 2-3: one rehearsal producing the same evidence
structure, serving prevention (before) and post-mortem (after).

## Out of scope

The behind-base refusal (deleted in E-1941 — do not resurrect it), the ledger
conflict itself (E-1943 / ED-1553, shipped as E-1955), fault-surface retention,
and adding `project_id` to `errors` (raised, deliberately not folded in).

## Verification

`tests/tasks/e-1957-verify.sh`, modeled on `tests/tasks/e-1941-verify.sh`:
pass/fail per check, summary, exit 0/1/2, fail-fast, `set -u` only — no bash
strict mode (house rule; load the `shell-script-author` skill first).

1. **The hazard is gone** — throwaway repo whose branch references a symbol
   deleted on base; drive a real land; assert the message contains neither
   numbered recovery and does name `endless worktree diagnose`. Fails on
   today's code.
2. **Evidence survives the abort** — after that failed land, `REBASE_HEAD` is
   recoverable from the capture even though the rebase was aborted.
3. **Each classifier** — one throwaway repo per row, asserting the class and
   that a prescription appears ONLY for the provable rows.
4. **The two unprovable classes prescribe nothing** — symbol supersession
   reproduces the E-1943 shape; semantic overlap is the explicit negative case.
5. **`--dry-run` predicts** — a branch that will conflict is reported by
   `--dry-run` with the same class the post-mortem gives; base, the task branch
   and the DB are unchanged afterwards, and the throwaway branch is gone.
6. **pytest** for the classifier units and the capture round-trip.

Project-wide regression (`just test`, `just test-go`, `just build`,
`just guide-check`) run by the implementing session and reported in one line.
