# Audit CLAUDE.md and propose a minimized version

## Motivation

The project `CLAUDE.md` has accumulated policy prose that isn't enforced by code,
restates defaults the model already follows, or sits beside a mechanism in a way
that invites wrong inferences. Concrete instance: the lifecycle clause implying
`spawn`/`claim` gate on `ready` (they don't) — see E-1816. The broader guidance
(and the owner's direction) is to keep CLAUDE.md lean and let the model handle
more, so agents aren't misled by over-specification.

## Deliverable

An audit of `CLAUDE.md` plus a **proposed** minimized rewrite (a concrete diff)
for owner review. This task does not silently rewrite CLAUDE.md wholesale —
minimizing is judgment-heavy and owner-reviewed; the deliverable is the proposal.

Classify each section/rule into:
- **Enforced** — backed by a code gate/hook/test. Keep (optionally trim to a
  pointer at the enforcing mechanism).
- **Restates a default** — behavior the model already does without being told.
  Candidate to cut.
- **Unenforced policy** — a convention with no gate (e.g. "background sessions
  claim only ready"). Keep only if it earns its space; reframe so it can't read
  as a mechanism.
- **Misleading / stale** — wrong, or invites a wrong inference. Fix or cut.

For each item recommend keep / trim / cut / reword, with a one-line reason, and
produce the proposed minimized `CLAUDE.md` as a diff.

## Verification

No runtime surface — the deliverable is a reviewed proposal, so there is no
per-task shell verify script. Verification is owner review of the proposed diff:
every retained rule is either enforced or an explicitly-labeled convention, and
nothing cut was actually load-bearing. Applying the approved diff (and re-syncing
any mirrored copies) happens after the owner signs off, in this task or a
fast follow-up.

## Scope

CLAUDE.md prose only. The canonical status-lifecycle mermaid block and other
byte-synced canonical blocks are out of scope (they have their own sync tests).
Do not touch global `~/.claude/CLAUDE.md` (not part of this repo).
