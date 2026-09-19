# E-1710 findings — Audit of main's committed ledger vs the real DB (WAL-loss check)

## Verdict

**No systemic WAL loss. The worktree-strand concern that prompted this task is refuted.**
Main's committed ledger reproduces the real DB's event-sourced rows, with exactly **two**
trivial post-rollout exceptions (E-1223, E-1224) that are stale placeholder rows, not a
worktree-strand. Every other "missing create" is the expected pre-event-sourcing baseline
(rows created before `task.created` emission existed, ~April 2026).

Read against `~/.config/endless/endless.db` (real DB) via `endless sql` (SELECT only) and
main HEAD (`531942a4`) committed ledger via `git show HEAD:`. **No mutations were made; `rebuild-db`
was never run** (verified read-only property of `validate-db`/`ProjectToTempDB` in code first).

## Scope checked
- Real DB: 948 `endless`-project tasks, 116 decisions. (Other ~8 projects' tasks are event-sourced
  in their own repos' ledgers, out of scope for endless's committed ledger.)
- Committed ledger (main HEAD): 4,450 events across 47 segment files, all `project=endless`.

## Forward check (the loss direction: DB row present, originating create absent from main)

Method: for each real-DB task row, require a committed `task.created` for that id (matched by
title to exclude test-fixture id collisions; `task.landed` also counts as proof of a real create).

- **255 task rows lack a committed create.** Broken down by `created_at`:
  - **253 are the pre-rollout baseline** — created **2026-04-14 … 2026-05-10**, overwhelmingly
    April. `task.created` emission was introduced during the late-April→May 2026 ledger rollout
    (E-1197/E-1198). Rows created before a given code path started emitting have no create event.
    This is a known, expected baseline, **not loss**. Month histogram of gaps: Apr=253, May=2,
    Jun=0, Jul=0.
  - **2 are genuine post-rollout gaps: E-1223 and E-1224** (see below).
- **Decisions: 8 lack a committed create**, all explained, none a post-rollout loss:
  - 934–939 (2026-04-28) and 933: pre-rollout legacy decisions (same era/baseline as the task gaps;
    933 additionally shows the legacy `task.created`→`task.deleted` extraction pair from E-1378).
  - D-1512 is a **false positive** — created as a normal task 1512 (committed `task.created` +
    `task.fields_updated` + `task.landed` all present) that later became a decision; covered.

### The one real finding: E-1223 / E-1224

| id | title | parent | created_at | status | committed events |
|----|-------|--------|-----------|--------|------------------|
| 1223 | Verify E-1209 V1 happy path retry | 1209 | 2026-05-10T00:30:57 | needs_plan | **none** |
| 1224 | Verify E-1209 V2 stale lock retry | 1209 | 2026-05-10T00:30:57 | needs_plan | **none** |

- A sibling pair created in the **same second** under parent E-1209 that **both failed to emit
  `task.created`**. Their earlier siblings 1220/1221/1222 (same parent, created 00:15 the same
  night) emitted normally, as do 1225+. No `source_file` (not an import). Never advanced past
  `needs_plan` (~8 weeks stale).
- **Not a worktree-strand**: no trace of their create on any branch, in git history, or in any
  working-tree ledger segment — searched `--all` + reflog. So this is an *event that was never
  committed anywhere* (create path didn't emit for that specific operation), not an event
  stranded on an unlanded branch. The mechanism the task feared (worktree emit landing on the
  wrong branch) is **not** what happened here.
- **Concrete consequence**: a full `rebuild-db` from the committed ledger would silently drop
  these two rows (they're the exact class of gap `validate-db` cannot see — see Tooling below).
  Impact is negligible: two dead placeholder subtasks of a long-finished verify effort.

## Reverse check (orphans: committed create with no live row) — clean

196 orphaned committed creates, fully accounted for: 18 have a committed `task.deleted` (legit),
96 are test fixtures (low ids), and the remaining **82 are all legacy `type=decision` creates now
living in the `decisions` table** (expected E-1378 routing). **Zero genuine orphans** — no risk of
`rebuild-db` resurrecting deleted rows.

## E-1709 "duplicate" — confirmed benign (the E-1713 amend pair, not a loss)

Main's committed ledger holds **exactly one** `task.created` for E-1709. The second copy the task
referenced lives in commit `3a3aa8b4` ("Endless: record ledger entry"), which is a **dangling
pre-amend commit** — no branch contains it and it is not an ancestor of main HEAD; the amended
version `7e207d8d` is what landed. So there is no event duplication in main's ledger — just an
orphaned git object that will be GC'd. This is exactly the `canAmend` scenario **E-1713** addresses.
E-1710 and E-1709/E-1713 should not be conflated.

## Secondary findings (not the WAL-loss question; recommend separate follow-ups)

1. **Test-fixture pollution of the committed ledger.** ~36 of the 47 committed segment files
   (~1,500 events, roughly a third of the ledger) are **test/e2e fixture ledgers** — they create
   fixture tasks ("smoke epic", "Coordinate breakdown epic", "Implement alpha/beta/gamma") reusing
   low task ids 1..~150, and got committed to main. Because they reuse real ids, a full
   `rebuild-db` would hit UNIQUE-id conflicts on ids 1..~150 (first-writer-wins + projection
   errors), corrupting low-id state. This is a real hazard for the rebuild path and likely part of
   why the plan's Safety section forbids running `rebuild-db` on the real DB. Fix direction: stop
   committing test-run ledgers (isolate test ledger dirs), and/or scrub the fixture segments from
   main's `.endless/db-ledger/`.

2. **`validate-db` blind spot.** `ValidateTasks` (internal/events/validator.go:54-70) iterates
   projected→current only and **deliberately skips the current-but-not-projected direction**
   ("Skip this for now — tasks created before event system won't have events"). That skip is what
   masks exactly the E-1223/1224 gap class. The skip was reasonable while the 253-row pre-rollout
   baseline would drown the signal, but it means `validate-db` cannot be relied on to catch genuine
   post-rollout gaps. Fix direction: report current-but-not-projected rows whose `created_at` is
   after the ledger-rollout cutoff (so the baseline is excluded but real gaps surface).

## Recommended follow-ups (to file with `--cleans-up E-1710` — pending your OK; not filed)
- **E-1223/E-1224**: delete the two stale rows (preferred — they're dead placeholders a rebuild
  would drop anyway) *or* backfill their `task.created`. Your call.
- **Fixture ledger pollution** (secondary #1) — the higher-value fix; makes `rebuild-db` safe.
- **`validate-db` current-but-not-projected detection** (secondary #2).
