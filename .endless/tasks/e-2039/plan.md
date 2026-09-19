# E-2039 — make an appeal able to restore an over-cut draft

## Problem

The appeal is presented to the agent as the recourse for an over-aggressive cut
("--raw prints it back unchanged, which is what makes an over-aggressive cut
recoverable"), but nothing implements restoration. `report_cmd` runs the appeal
through the SAME minimize prompt, with no separate prompt and no reference to
the pass being appealed. It is simply a second, independent minimization whose
input happens to contain an argument — free to return LESS.

Observed twice in one session: pass 1 deleted the answers to two directly-asked
questions; the appeal deleted those AND the appeal argument itself, leaving
disconnected fragments. Budget is one, so there was no third try.

## Why this is not a prompt fix

Monotonicity is a property of the RELATIONSHIP between two outputs. A prompt can
ask for it and cannot guarantee it, and the optimizer can only tune wording. The
existing invariant vetoes are in Python for exactly this reason.

## Approach

Give the appeal a different job from the first pass. It is not "minimize again";
it is "reinstate specific material into an existing minimization."

1. **The appeal sees the first pass.** Build a distinct `appeal` prompt entry
   (new key in `report_prompts.DEFAULTS`, so it is overridable like the others)
   taking the raw draft, the FIRST pass's output, and the agent's argument. Its
   instruction is to return the first output plus the material the argument
   justifies — not to re-edit from scratch.

2. **Mechanical floor.** After the appeal returns, verify it is a superset of
   the first pass on protected content (`minimizer_invariants.has_protected_
   content` already identifies code blocks, tables, commands). If the appeal
   dropped protected content the first pass kept, reject the appeal and keep
   pass 1 — a failed appeal must never be worse than not appealing.

3. **Length floor.** Reject an appeal output shorter than the pass it appeals.
   Blunt, but the appeal's whole premise is that something was missing; a
   shorter result is definitionally not that. Cheap, no model call, no judgment.

4. **On rejection, say so.** Print pass 1 with a one-line note that the appeal
   was refused and why. Silence here would read as "the appeal ran and this is
   its output", which is the confusion that makes the current behavior invisible.

## Corpus

An appeal already writes its own row. Record which row it appeals (`appeals_
gate_id`, nullable) so the pair is queryable — today an appeal is
indistinguishable from an ordinary turn, which is also why offsets into draft
history are ambiguous (see E-2040).

## Verification

- An appeal that argues for a cut paragraph returns pass 1 PLUS that paragraph.
- An appeal whose output drops a code block or command present in pass 1 is
  rejected, and pass 1 is printed instead.
- An appeal shorter than pass 1 is rejected.
- A rejected appeal still consumes the budget (it ran) and says it was rejected.
- The appeal row records the gate it appeals.

## Open question

Whether a rejected appeal should refund the budget. Argument for: the agent got
nothing. Against: it spent a model call, and refunding invites retry-until-lucky
— the exact behavior the one-appeal bound exists to prevent. Default to no
refund unless the user prefers otherwise.
