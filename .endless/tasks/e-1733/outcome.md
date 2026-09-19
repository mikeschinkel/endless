# E-1733 brainstorm results — ledger-target routing policy

Decided with Mike, 2026-07-04. Works from E-1732's factual map.

## Decided policy

1. **The ledger lives next to whichever DB is active.** Main DB → real project ledger, auto-committed on main at write time (unchanged; E-1206/ED-901). Sandbox DB → `db-ledger/` inside the sandbox dir, next to its `endless.db`.
2. **The sandbox ledger never touches git.** It sits in the cache dir outside any repo — nothing to commit, no merge machinery, erased atomically with `sandbox destroy`. The DB and its WAL live and die together.
3. **One in-code resolver decides the ledger target** — root directory plus commit-or-not, resolved once alongside the existing DB-context resolution. All four `CommitLedgerSegment` callsites (E-1729's draft says three; the epic-derivation emitter is the fourth) and both `ProjectToTempDB` callsites ask it. No flag, no env var, no config file.
4. **Reads are symmetric**: `validate-db`/`rebuild-db` replay whatever ledger the active DB context resolves to — main context → real ledger, sandbox context → sandbox ledger.
5. The stale comment at `internal/events/commit.go:135-138` (claims sandbox ledger routing that never existed) gets corrected as part of the implementation.

## Per-use-case behavior under the policy

- **Real emits** (main checkout, CLI or hook-attributed): unchanged — real ledger, write-time auto-commit on main.
- **Self-dev worktree session routed to the sandbox**: DB row → sandbox `endless.db`; ledger line → `<sandbox>/db-ledger/`, no commit.
- **`worktree land`**: unchanged (runs in main DB context) — auto-commit globs, orphan-drop, `task.landed` emit to the real ledger.
- **`validate-db` / `rebuild-db`**: follow the resolver; sandbox rebuilds replay only sandbox-born events (the sandbox DB is seeded near-empty, so that is the complete correct history for it).
- **Test harnesses**: unchanged — throwaway `git init` repos with `--config-dir` tempdirs are not sandboxes (not under `sandboxes/`), so they keep real commit behavior.
- **Multi-dev sync**: unaffected — only the real ledger is committed and synced; sandbox ledgers are node-local and disposable.

## History corrected

Sandbox emits landing in the real ledger was never decided. No decision on file says so; the `commit.go` comment asserting sandbox ledger routing was written by a prior agent session and was never true at any point (`writer.go` has derived the ledger root from the registered project path since E-805). Verified empirically: ~35 live sandboxes' node ids match segment files in the real ledger (e-1567: 156 lines, e-1601: 101, e-1541: 97, ...) while zero ledger files exist inside any sandbox. What Mike remembered as deliberate was E-1309 (worktree emits write/commit to main to avoid rebase conflicts — still correct, unchanged) and skip-worktree on `.claude/settings.json` (E-998, unrelated to ledger files).

## Rejected options, with reasons

- **Status quo** (sandbox emits → real ledger): the bug. 41 foreign node ids, ~1,071 lines, ~23% of the real ledger.
- **Worktree-hosted sandbox ledger**, including the `.gitignored` `db-ledger/sandbox/` subdir variant: lives inside a checkout of the real repo, so it is guarded-against rather than structurally unable to ride the land rebase into main; splits the DB from its WAL lifecycle-wise (worktree drop vs sandbox destroy are independent). The subdir variant is functionally the sandbox-dir option with git standing nearby for no reason. Mike concurred after working the variant through.
- **DB-only sandbox emits** (no ledger at all): breaks events-authoritative — a sandbox could never `rebuild-db`, and self-dev sessions would not exercise the write path they are usually developing.
- **Repoint the sandbox's `projects.path` at the sandbox dir**: re-routes with zero Go changes but lies about where the project lives, `path` serves other purposes, and the commit path would hard-fail on a non-repo.
- **`git init` each sandbox** to keep the commit path uniform: commits nobody ever reads; breaks sandbox-is-a-plain-dir; the commit machinery is already exercised by the throwaway-repo harnesses.

## Cleanup of the existing pollution (decides E-1730's approach)

**Excise now, once, as a bug cleanup.** The leaked lines were written by a routing bug and were never legitimate history; delete them from the real segment files in a normal hygiene commit on main (file edit, not git-history rewrite). **Append-only remains the rule for legitimate history going forward.** Tombstoning (E-1716/E-1720) is reserved for events that were real but wrong — its natural home is the multi-dev future. This resolves the E-1730-vs-E-1716 tension: they are different tools for different classes of bad events, not competing answers to the same question.

Constraints on the excision:

- **Per-line classification, not per-node.** Some foreign-node segments interleave real events (`task.landed` for E-1542 and E-1628 sit inside otherwise-junk segments), and sandbox `worktree-e-1507` shares node id `a7f3` with the real node, so its emits hide inside real segments.
- **Ordering (raised by Mike): land the routing fix before excising.** Existing live self-dev worktree sessions run pre-fix binaries and keep writing sandbox emits into the real ledger until E-1729 lands and those sessions end or rebuild. E-1730 is already blocked by E-1729, which encodes the ordering. A second sweep after the fleet turns over catches stragglers.
- **No per-worktree fixing is needed beyond that.** Retained worktrees hold polluted committed copies of the segments, but (a) no read path ever reads a worktree's ledger copy — `validate-db`/`rebuild-db` resolve the registered main-checkout path; (b) branches don't carry ledger commits, so the land rebase replaces a worktree's ledger state with main's excised state for free; (c) dropped worktrees take their copies with them.

## Boundary with E-1665

The routing fix makes sandbox-context pollution impossible by construction. It does NOT cover a hand-run `event emit --project-root <real repo>` from a non-sandbox context — that hazard remains E-1665's scope (safe-by-construction testing of the emit path).

## Follow-ups spawned

- Decision: ledger target follows the active DB context (decides E-1729).
- Decision: excise-once cleanup, append-only protects legitimate history (decides E-1730).
- Task: land-time gate — refuse `worktree land` when the task branch's commits touch `.endless/db-ledger/` (cleans up E-1733).
- E-1729's re-plan is deliberately NOT done here (awaits Mike's go-ahead). Its planner should absorb: the resolver shape, 4-not-3 commit callsites, read symmetry, and the stale-comment fix.
