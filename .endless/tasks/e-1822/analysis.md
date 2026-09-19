# Design captured (delivery arm PENDING E-1803)

Two separable axes. Axis 1 (content and templates) is independent and plannable
now. Axis 2 (delivery) rides on E-1803's PostToolUse mechanism and must NOT be
finalized until E-1803 is implemented and proven.

## Decision: uniform relocation

A claimed-in session ALWAYS relocates its working directory into the task's new
worktree -- no type-dependent exception. Rationale: staying in the main checkout
risks running tools against the main branch instead of the worktree branch (the
worse failure). No-code tasks are not no-touch: brainstorm and research record
their outcome to the git-tracked db-ledger, which must be merged -- so they too
produce a landable artifact and belong in the worktree. The wrapper's job is to
make the relocation and the resulting --db main routing impossible to forget
(that routing switch is exactly the thing that must not be dropped).

## Axis 1 -- content (plannable now)

Split each type's handoff into (a) invariant type-mechanics and (b) arrival
framing. Shape: a shared partial plus per-context wrappers (the spawn wrapper is
the existing framing; the claim wrapper is new). Discipline: each partial earns
its existence from a genuinely invariant shared concern; reach for a conditional
only when the alternative is three-plus near-identical partials; lift to shared
DATA only where a drift between renderings would be a bug (terminal status, the
one-session-one-task rule); do not pre-factor -- let the second real consumer
pull each seam.

## Claim wrapper content (arrival block, claim-only)

Focused, not thin. Beyond the shared type-mechanics partial it must carry:
- you are NOT freshly spawned; you have been running and just claimed this -- no
  blank-slate assumption;
- relocate: move your working directory into the task's new worktree (a spawned
  session is born there; a claimed-in one is not);
- routing: the worktree routes DB access to the sandbox, so real-ledger updates
  need --db main;
- fold the planning you already did into the task (the task update text field),
  not left in chat.

## Axis 2 -- delivery (PENDING E-1803)

Candidate: a PostToolUse hook matched to the endless task claim invocation
returns hookSpecificOutput.additionalContext carrying the claim wrapper, folded
inline against that tool result (the same lever E-1803 prototypes for task
report). NOT settled -- E-1803 is unbuilt and its own analysis rates the
mechanism a strong nudge, not a hard gate. Finalize only after E-1803 lands and
the mechanism is proven; if E-1803 finds it weak or unworkable, redesign
delivery here.
