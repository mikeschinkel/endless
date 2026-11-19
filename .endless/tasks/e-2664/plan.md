# Plan

Answers from Mike on 2026-11-19 (Q1–Q5) settle the design; the items are the contract.

## Type and brief
- `tasktype`: add `TaskTypePlan` (id 6, slug `plan`, label `Plan`) with schema seed row and migration. Not auto-spawnable: the auto-spawn job needs `ready`, a rating and a plan, so a plan task never qualifies; it spawns at filing instead. Does not land.
- `taskcontent`: add `Brief`, displayed after Justification and before Context. Flag `--brief` / `--brief-file` on add and update (the existing `task show --brief` preview flag is on `show` only, so names do not collide; checked during implementation).
- `task add --type plan` requires a brief. `task update --type plan` is allowed from an unplanned todo/bugfix with a brief, and records the former type as the proposed `--becomes`.

## Status while planning (Q1 = A)
- Spawn of a plan task follows prime's launch path: worktree created, session bound by its cwd, nothing claimed, status stays `unplanned`. The window opens in the task's own project (E-2405 logic).
- The planning session attaches the plan and items (→ `submitted`) with `--becomes`, `--complexity`, `--risk`.

## Eventual type (P6)
- Stored as a task column (`becomes_type_id`) carried on events, proposed like ratings, ratified at `task approve` (`--becomes` on approve changes it). Only todo/bugfix.

## Approval and handoff (Mike's design for Q2)
- `task approve` on a plan task: converts type → `--becomes`, approves as usual (→ `ready`), then asks "send the <type> handoff to the planning session?" (`--yes` skips). On yes, render the type's claim handoff and run `claude -p` instructing it to deliver that text with SendMessage to the planning session. The handoff tells the session to `endless task claim`. A failed delivery leaves the approval in place and prints the handoff path and the claim command.

## Gate (Q4 = D)
- `_require_spawnable`: plan, research and brainstorm pass with a non-empty brief and no plan; `context` no longer counts for research/brainstorm; epic keeps context (E-2254).
- Post-land: loop over the unstarted research/brainstorm tasks framed only by context and give each a generic brief: "This task predates briefs. Its framing is in Context; read that as the brief."

## Handoff and guide (Q3 = A, Q5 = B + some A)
- New `handoff/plan.md.tmpl`: read the task, read `endless guide tasks/planning`, put open questions in chat per those rules, then write plan + items and submit with `--becomes` and ratings; then wait for approval and the handoff.
- New guide subsection `tasks/planning` carrying the /needs-plan rules (brainstorm branch dropped — plan tasks are never brainstorms). The handoff restates the rules in a few lines.
- Guide: filing a plan task (`endless task add "<title>" --type plan --brief "..." [--context ...] [--analysis ...] [--becomes todo] [--no-spawn]`), what a brief is (objective, required context, success criteria only if the user stated them; no scope list, no options), and the research/brainstorm brief rule. Regenerate guide index and lifecycle artifacts as needed.

## Tests
- Go: tasktype enum/integrity, taskcontent order, handoff template render for plan.
- Python: add/update/spawn gates, --becomes validation, approve conversion and the send prompt (claude -p mocked), research/brainstorm brief gate.
