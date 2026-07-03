# E-1722 (research) — Triage MEMORY.md lessons for endless feature or guide-doc fit

**Findings-only research.** This task determines *which* memory entries look like
good fits for conversion and categorizes them. It does NOT decide dispositions,
design the fix, or file per-candidate tasks — that is E-1723's brainstorm, which
consumes this output.

## Input

All 139 entries in this project's agent-memory store at
`~/.claude/projects/-…-endless/memory/` — the `MEMORY.md` index plus the `*.md`
topic files (mostly `feedback_*`, some `project_*` / `reference_*`).

## Per-entry classification

For each entry, determine the best-fit category:

- **(a) endless-feature fit** — a behavior that could be gated/enforced by the
  product (hook / CLI / schema), so every endless user gets it, not just Mike's
  memory. (Epic thesis: `endless-fixes-ship-in-product-not-my-memory`,
  `ship-in-product-not-machine`, `gates-not-guardrails`.)
- **(b) guide-doc fit** — belongs in `endless guide` as durable guidance.
- **(c) stays memory** — pure agent-behavior feedback with no product surface.
- **(d) stale/obsolete** — no longer true; note as a pruning candidate.

Some entries may plausibly fit more than one; record the primary fit plus a note.

## Surface hints (to judge feasibility of an (a)/(b) fit — not to design it)

- *Feature, tool-time gate:* `internal/hookcmd/claude.go` (`handlePreToolUse` +
  `blockToolUse`/`preToolUseBlock`; existing gates: claim-before-write,
  `blockCommitOnMainIfApplicable`, `blockSqliteAgainstEndlessIfApplicable`,
  revisit gate).
- *Feature, CLI-time check:* `src/endless/task_cmd.py` (`validate_title`,
  `validate_description`, status guards).
- *Feature, new enum/lookup:* `internal/schema/changes/*.sql` + a Go
  `internal/<kind>/` validator (no CHECK constraints).
- *Guide:* `docs/guide/*.md` body + `docs/guide/help/*.md` map file, regenerated
  via `/regenerate-guide` (`src/endless/guide_map.py`).

## Deliverable (in the outcome)

A triage table: **entry · category (a/b/c/d) · feasibility note · rough priority**.
No dispositions decided, no follow-up tasks filed here — that is E-1723.

## Known seeds (start the table with these)

- `endless-fixes-ship-in-product-not-my-memory`, `ship-in-product-not-machine`,
  `gates-not-guardrails`, `just-is-dev-only` — likely **(b) guide** and/or the
  meta-principle behind many (a) candidates.
- The "epic status is auto-derived; agents shouldn't flag its changes" lesson —
  **(b) guide** candidate. (The `(auto)` *display* half is already filed as E-1725.)
- Index-size / partial-load lessons — flag as **better handled by E-1724's
  maintenance model** than a per-lesson conversion.

## Scope guard

Endless-scoped. Do not generalize into multi-project memory tooling (deferred).
