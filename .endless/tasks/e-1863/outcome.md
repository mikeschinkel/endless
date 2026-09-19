# Task-relationship semantics — working agreement (2026-08-03)

Captured here (in a task outcome) rather than the decision table, because decisions
are moving back onto tasks (E-1861). This supersedes proposed decisions **ED-1517**
(relationship taxonomy) and **ED-1514** (bugfix diagnosis child), which are being
rejected in favor of this capture.

## Decided

- **Parent-child means different things by parent type:**
  - **Epic parent = containment.** An epic is never directly implemented; its
    completion derives entirely from its children's states; it has no deliverable
    of its own. (per accepted ED-1503)
  - **Non-epic parent (todo / research / bugfix / brainstorm) = grouping/provenance,
    NOT containment.** A non-epic parent may reach a terminal status while it still
    has incomplete children. The relationship is for grouping and lineage, not
    completion-derivation.
- **Open children must always surface** in "what to work on" views regardless of an
  ancestor's terminal status. This is a surfacing/filter requirement, not a modeling
  one — grouped children must never be hidden or lost behind a completed parent.
- **Consequences:**
  - `research → bugfix` as a child is legal and low-ceremony (research completes on
    findings; the fix is a grouped follow-up child that keeps surfacing). No epic
    wrapper required.
  - `bugfix → research(diagnosis)` child is also legal. Neither direction is
    mandated; ED-1514's prescription is subsumed by this general model (hence its
    rejection).
  - **Discriminator for "should this be an epic?":** does the parent have a
    deliverable of its own? Completion == sum of children with no parent-level
    deliverable → epic (container). Parent has its own standalone deliverable and
    merely has a subordinate child → legitimate non-epic-with-child.
- **Epic-ness is defined by container-ness, not size/duration.**

## Open (undecided)

- Exact surfacing/filter behavior for open descendants under a terminal-status
  parent — where it applies (session status, task next, list views) and how.
- Whether/how to add a promote-to-epic prompt/gate at `task add --parent` /
  `task move` when a child is added to a non-epic that has no deliverable of its own.
- Durable documentation home: epic + relationship semantics belong in `endless guide`
  (E-1545 covers the epic definition) — this outcome is interim capture, not the
  permanent home.
- Interaction with the decision-subsystem rework (E-1861): formalization rides that
  rework; until then this task-held capture is the working agreement.
