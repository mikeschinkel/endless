# Research brief: endless's event-ledger target routing + git-commit constraints

## Why
E-1729 surfaced that `--db sandbox` isolates the DB but NOT the ledger, so
dev/test emits pollute the real `.endless/db-ledger/`. The naive fix ("sandbox
skips the git commit") breaks the <1% case where we are testing endless's OWN
ledger/commit machinery and therefore NEED a real, committing, isolated ledger.
Before deciding the policy (a follow-on brainstorm), map the FACTS.
**Deliver facts, not a recommendation** — "what should we do" is the brainstorm's job.

## Questions to answer (facts, each with file.go:Symbol references)
1. **Write paths.** Every code path that appends to a ledger and/or git-commits a
   segment: `events.NewWriter` callers, `events.CommitLedgerSegment` callers,
   `worktree land` auto-commit (commit.go / canAmend), the hook binary, the
   channel MCP, anything else. For each: how it resolves (a) the ledger dir,
   (b) the project root, (c) whether/where it commits.
2. **Read paths.** `events.ReadAllEvents` / `events.ProjectToTempDB` callers
   (`validate-db`, `rebuild-db`, projection): how each resolves the ledger location.
3. **Location resolution, end to end.** Python `emit_event` project_root (projects
   table) + `--config-dir` / `go_db_context_args`; Go `monitor.ConfigDir()` /
   `IsSandboxActive()` / `--project-root`; and how DB location vs ledger location
   relate today.
4. **Git-commit coupling (E-1206).** What `CommitLedgerSegment` does and why; what
   depends on the commits (canAmend rewrite, `worktree land`, backups, recovery,
   `just land`); what breaks if a ledger dir is not a git repo or commits are skipped.
5. **Sandbox mechanics (E-1281).** What a sandbox isolates today (DB via XDG /
   config-dir) vs not (ledger); how sandboxes are created/destroyed; whether the
   sandbox dir is a git repo.
6. **Event-sourcing invariants.** What "events-authoritative" requires (ledger =
   source of truth, DB = projection) and what `rebuild-db` / `validate-db` assume,
   so any isolation option can be checked against them.
7. **The <1% product-testing case.** How endless's own ledger/commit code is tested
   today (e.g. `tests/tasks/e-1719-verify.sh`'s throwaway `git init` project root);
   what those harnesses need from the ledger (real commits). Catalog the patterns.
8. **Pollution evidence.** Quantify the sandbox events already in the real ledger
   (count, node ids, id ranges) — grounds E-1730 and shows blast radius.
9. **Prior decisions.** What E-1281, E-1206, E-1368, E-1513, E-1719, E-1729, E-1730
   each already locked in about ledger location/commit, and whether ledger isolation
   was ever discussed.

## Deliverable (in the task outcome)
A factual map answering 1-9 with `file.go:Symbol` references, plus a plain
statement of the **decision space** the facts imply (the distinct options for
"which ledger does an emit target, and who decides") WITHOUT choosing one. That
map feeds a follow-on `brainstorm` task where we decide the policy, which then
re-plans E-1729.

## Out of scope
- Deciding the policy (brainstorm's job) or recommending an option.
- Implementing anything / touching the ledger (E-1729 fix, E-1730 cleanup).
