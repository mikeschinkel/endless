# Brainstorm: ledger-target routing policy

This is a discussion task. The deliverable is the brainstorm's results in this task's
outcome — the decided policy, per-use-case behavior, rejected options with reasons, and
any genuine conflicts. Do not implement anything.

## Step 1 — Read the research

Read E-1732's findings first: `endless task show E-1732 --outcome --db main`.
That is the factual map: every ledger write/read path, the git-commit coupling and its
dependents, sandbox mechanics, event-sourcing invariants, the test-harness patterns,
the measured pollution, prior decisions, and the decision space (axes A–D: where a
sandbox/test emit's ledger line lands; commit behavior for a non-real ledger; who
decides the target; the real-commit testing case).

## Step 2 — Discuss with Mike

Work the decision space with Mike in chat, not solo:

1. For every approach in the decision space (and any new approach that surfaces in
   discussion), lay out the pros and cons.
2. For each approach, explain concretely how the ledger should and will work in each
   use-case:
   - real emits from the main checkout (CLI and hook-attributed)
   - emits from a self-dev worktree session routed to a sandbox (`--db sandbox` /
     cwd self-detect)
   - `worktree land` (auto-commit of ledger dirt + the `task.landed` emit)
   - `validate-db` / `rebuild-db` in both main and sandbox contexts
   - test harnesses that exercise the emit/commit machinery (today: throwaway
     `git init` repos as `--project-root`)
   - multi-dev / multi-node sync via committed segments
3. Identify any situations that are truly in conflict — requirements no single
   approach can satisfy simultaneously — versus ones that are merely undecided, and
   state them explicitly. Include the cleanup tension the research surfaced: E-1730's
   excision vs E-1716/E-1720's append-only tombstoning.

## Step 3 — Record and hand off

Write the brainstorm results into this task's outcome. Then the decided policy
re-plans E-1729 (and informs E-1730's cleanup). Do not re-plan E-1729 without Mike's
explicit go-ahead in the discussion.
