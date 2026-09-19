# E-1710 — Audit main's committed ledger for gaps vs the real DB (WAL-loss check)

## Context
During the E-1542 session a concern was raised that events emitted from a worktree with
`--db main` might strand their ledger (WAL) entries on the worktree branch instead of reaching
main — i.e. real-DB rows whose originating events never land in main's committed ledger. A
code trace largely refuted the mechanism: `ensureMainCheckout` (in `internal/events/commit.go`)
prevents the emit path from ever committing a ledger entry onto a worktree branch, and the one
observed "duplicate" (E-1709's `task.created` appearing on both main and a worktree branch) was
the benign pre/post-amend pair of a single commit — the `canAmend` behavior that **E-1713** now
fixes. So this task is no longer a bug hunt; it is a **confirmatory audit**: prove, with a
systematic scan, that main's committed ledger has no gaps versus the real DB. Deliverable is
findings, not a fix.

## Safety — the real/main DB and ledger are READ-ONLY for this audit (non-negotiable)
This task inspects; it must not mutate. Two hard rules:
- **NEVER run `endless-go event rebuild-db` against the real/main DB.** `rebuild-db` REPLACES the
  live `tasks` table from a replay — running it on `~/.config/endless/endless.db` would clobber
  the real ledger's projection. It is not part of this audit at all.
- The only Endless reads allowed against main are **read-only**: `endless sql` (SELECT only) and
  `git show HEAD:...` on the committed ledger. If a full replay is needed to compare, replay into
  an **explicit throwaway temp DB** (copy the ledger / point at a `mktemp -d` config dir), never
  the real DB path. Confirm in code that `validate-db` replays into a TEMP DB and does not write
  the live DB before relying on it; if there's any doubt, operate on a copy.

## Method
1. **Prefer the read-only comparator.** `validate-db` (Go, events/eventcmd surface, driven by
   `replayEvent` in `internal/events/projector.go`) replays main's committed ledger into a TEMP
   DB and diffs against the live DB without mutating it — verify that read-only property in the
   code first, then run it against main's committed ledger and capture the mismatches. Do NOT use
   `rebuild-db` (see Safety). If `validate-db` turns out not to be strictly read-only, fall back
   to a manual replay into a `mktemp` DB.
2. **Know the projector's coverage.** Read `replayEvent` to list which event kinds it replays
   (task create/import/status/fields/move/delete/landed, decisions, epic-status-derived, etc.)
   and which it deliberately skips (session-scoped kinds like `task.claimed`/`task.released`
   are ephemeral and NOT part of the task snapshot). The audit's "gap" definition is scoped to
   the replayed, snapshot-defining kinds — a row that the projector *should* reproduce but
   doesn't is a real gap; a skipped session-kind is not.
3. **Direct cross-check for the entity types that matter.** For `tasks` (and `decisions`) in the
   real DB, confirm each row's originating event (e.g. `task.created`) is present in main's
   COMMITTED ledger (`git show HEAD:.endless/db-ledger/*.jsonl`, not the working tree). Report
   any row whose originating event is absent from main's committed ledger — that is the
   worktree-strand / loss the concern feared.
4. **Reverse check (lower priority):** ledger events with no corresponding live-DB row
   (orphaned events) — note but don't treat as loss.

## Deliverable (in the task outcome)
- Whether any real WAL gap exists (row present in the real DB, originating event absent from
  main's committed ledger). If clean: state so plainly with the counts checked.
- If gaps are found: enumerate them and file a SEPARATE follow-up fix task (`--cleans-up` this
  task) — do NOT fix inline (audit tasks deliver findings; the fix is a separate task).
- A one-line confirmation that the observed E-1709 "duplicate" is the benign amend pair E-1713
  addresses, not a loss (so the two tasks don't get conflated).

## Verification
The only thing handed to Mike after implementation:

    esu && ./tests/tasks/e-1710-verify.sh

For a findings task there's no code to exercise, so the script is simple — it verifies the
*deliverable exists and is wired up*, not behavior:
1. The E-1710 `outcome` is non-empty (findings were recorded) and states a clean-vs-gap verdict.
2. If the audit found a gap, the follow-up fix task is linked to E-1710 via `cleans_up`
   (assert the relation); if it found none, that "clean" verdict is present in the outcome.
3. Prints `ALL PASSED` (exit 0) or a failure list (exit 1). Reads the DB **read-only**
   (`endless sql --db main`, SELECT only) — consistent with the Safety rule above.

This keeps every task, audits included, behind the single `esu && ./tests/tasks/e-NNNN-verify.sh`
handoff.

## Scope / notes
- Findings-only. E-1713 owns the amend fix; this task does not change `canAmend` or the ledger
  commit path, and does not run `rebuild-db` (see Safety).
- Close by recording the outcome (not `assumed`, since the deliverable IS the findings, not
  deferred verification).

## Critical code sites (by symbol)
- `validate-db` / `rebuild-db` subcommands and `replayEvent` in `internal/events/projector.go`
  (the replay/compare engine — the audit's primary tool and the source of truth for which kinds
  count).
- `ensureMainCheckout` and `canAmend` in `internal/events/commit.go` (context for why a
  worktree strand can/can't happen; already traced — no change here).
