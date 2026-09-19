# Defer end-of-session recap to `session status` (PRODUCT fix) + dirty indicator

## Why
End-of-session agent recaps restate task status/phase/relationships the user
already gets from `endless session status`. That duplication wastes tokens and
adds to the information overload Endless exists to reduce. The guidance must ship
in the PRODUCT so every Endless user's agent gets it — a per-user agent memory
only helps that one user.

## Part A — tighten handoff templates (primary deliverable)
- Files: `internal/templatecmd/templates/handoff/*.md.tmpl` — task, epic, bug,
  research, brainstorm, respawn.
- Current `Final message:` line: "a 1–2 sentence summary, the how-to-test, and
  this return line." Agents over-expand "summary" into a task-status bullet list.
- New intent: surface ONLY what `session status` can't show — git/worktree
  side-effects (dangling tags, uncommitted changes, stray artifacts), the
  landed-vs-worktree delta, and state outside endless (CI, external services) —
  plus the how-to-test and return line. Explicitly: do NOT recap task
  status/phase/relationships.
- Consider a companion note in the `orchestration` guide section.

## Part B — landed/dirty indicator in `session status` (exploration)
Goal: make the landed-vs-worktree delta command-provided so the agent never needs
to narrate it (see Part A's "surface only what `session status` can't show").
- Per session task, determine: is it landed? If landed, does the worktree have
  changes since the land (needs re-landing)?
- Collapse to a single "dirty" indicator: render `◆` (U+25C6 BLACK DIAMOND;
  UTF-8 E2 97 86) instead of the space between the task-type letter (E/T/B/…) and
  the id in the FLAT view — e.g. `T◆E-1684` — when not landed / dirty. NO legend
  entry (Mike, 2026-07-01).
- Distinct from `--tree`'s focal marker: the flat `◆` (dirty) and `--tree`'s
  leading `*` (the session's active/"this" task) are different views and glyphs —
  no clash. Open refinement (Mike): in `--tree`, switch the "this" marker from
  `*` to `●` (the flat view's `● this` icon) for cross-view consistency, and pick
  a distinct "landed" glyph if one reads well.
- Data source: ledger `task.landed` event for "landed"; git worktree HEAD vs
  merge-base (or vs the landed commit) for "dirty".
- Open: if Part B warrants real implementation, split it into its own task after
  the exploration concludes.

## Verification
Deliver `tests/tasks/e-1690-verify.sh` following the established ad-hoc convention
(model: `tests/tasks/e-1577-verify.sh` / `e-1684-verify.sh`; the per-task verify
framework is being formalized under E-1596 — treat those scripts as shape/output
examples, not this task's deliverable). Bash; sandbox/temp DB; the
`section`/`report_pass`/`report_fail`/`summary` + `assert_*` helpers; prints
`ALL PASSED` or a `FAILED` list; exits 0 on all-passed / 1 on any failure;
runnable from anywhere in the worktree.

It must verify, per part implemented:
- Part A: the handoff templates' `Final message:` line no longer instructs a
  task-status recap and DOES instruct "surface only what `session status` can't
  show" — assert the new wording is present and the old is gone across the
  `internal/templatecmd/templates/handoff/*.md.tmpl` set.
- Part B (if built): a landed task whose worktree has changes since the land
  renders the `◆` dirty indicator between the type letter and id; a clean landed
  task does not.

The verify-handoff instructions for this task must be EXACTLY these two lines,
nothing more:

    esu
    ./tests/tasks/e-1690-verify.sh

If that can't be the whole handoff, the script is incomplete — fix the script,
not the handoff.
