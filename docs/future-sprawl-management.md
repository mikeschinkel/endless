# Future: Document Sprawl Management

Captured 2026-04-08. Needs significant design discussion before implementation.

## The Problem

Claude (and other AI tools) habitually create new documents instead of updating existing ones, leading to proliferation:
- `PLAN.md`, `plan-v2.md`, `implementation-plan-phase-3.md`
- `COMPETITIVE_POSITIONING_AND_BMC.md`, `..._REVISED_TAKE_2.md`, `..._TAKE_3.md`, `..._TAKE_4.md`
- Multiple README.md files across subdirectories

## Important Distinction

Not all "multiple files of the same type" are sprawl:
- **Intentional versioning** — e.g., `TAKE_<n>` naming for research iterations downloaded from Claude Web conversations. These are deliberate point-in-time snapshots.
- **Subdirectory READMEs** — e.g., `cmd/README.md` alongside root `README.md`. These serve different audiences.
- **ADRs and research reports** — inherently plural by design (one per decision/topic).
- **Unintentional proliferation** — Claude creating `design-brief-updated.md` instead of updating `DESIGN_BRIEF.md`. This is the actual problem.

## Design Principles

1. **One canonical file per singleton type** — README.md, PLAN.md, DESIGN_BRIEF.md, CHANGELOG.md, CLAUDE.md
2. **Endless should drive standardization** — nudge toward canonical names, offer to consolidate
3. **History via hidden git repo or SQLite** — track when documents were created, updated, superseded. No need for `_v2` naming when the tool manages history.
4. **Archiving, not deletion** — obsolete docs move to `.endless/archive/` with timestamps

## Open Questions

- How to distinguish intentional versioning from sprawl?
- Should Endless enforce naming conventions or just suggest?
- How aggressive should consolidation suggestions be?
- What role does the hidden git repo play vs SQLite for document history?
- How does this interact with the web dashboard view?

## Example: ~/Projects/homelab

A real case with 60+ markdown files including research iterations (TAKE_1 through TAKE_4), multiple design briefs, and scattered READMEs. This is the canonical example for designing sprawl management.
