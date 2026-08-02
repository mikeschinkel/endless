# README rework

Today README.md is largely a CLI reference (roughly the Project Management, Task Management, Documents, Web Dashboard, Hooks sections). Refocus it on onboarding a new human.

## Keep and lead with
- What Endless is — the elevator description (task tree, decisions as first-class artifacts, per-task worktrees, session tracking, optional enforcement, dashboard).
- Install — the install steps.
- Getting started — the minimal first-use path.

## Move out
- The exhaustive CLI reference and broad how-to-use material. Link to the docs guide index for deeper use.
- Note that the guide is written for a Claude Code session but humans still benefit from it.

## Link out to the contributor docs
- Link to `ROADMAP.md` (the exists/planned contributor roadmap) and `VISION.md` (the envisioned-end-state narrative). Keep the pointers short; do not inline their content — that bulks the README. (Folded in from a separate slim-README follow-up that this task supersedes.)

## Keep in sync
- The canonical status-lifecycle mermaid block is embedded byte-identical in README.md, CLAUDE.md, and docs guide index between the canonical markers; preserve that invariant (a verify script asserts they match).
