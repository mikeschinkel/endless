# The corpus that already exists

`.endless/LESSONS.md`: 3265 lines, 129 entries. Each one is a correction Mike
gave, written at the moment he gave it, in his framing rather than a model's
paraphrase of it. Nothing reads it.

The minimizer loop is meanwhile short of exactly this. It judges its own output
with a model, and calibrates that model against whatever reaction it can detect —
a scarce and indirect signal. Here is a dense, deliberate, already-written record
of what he considers wrong with agent output, sitting in the repository.

# What it is NOT

Not the memory feature, which is off here on purpose (an agent's own context
papering over defects Endless exists to surface). LESSONS.md is write-only for
agents by rule. Feeding it to the MINIMIZER is a different act: the minimizer is
a second party editing someone else's draft, not a session recalling its own
past. Nothing in the memory-off rationale argues against it, but the distinction
should be stated explicitly wherever this lands, because it looks like a
violation at a glance.

# Three uses, cheapest first

1. **Seed the denylist.** Several entries name phrasings directly. Mechanical,
   auditable, no model call.

2. **Evidence for the generator.** `minimizer_optimizer.evidence()` already
   assembles what the champion is getting wrong from judge complaints and user
   labels. Lessons are the same shape and better sourced.

3. **An eval set.** Entries that quote a bad reply and say why are labelled
   examples. A candidate instruction that would not have prevented a recorded
   complaint is a candidate that has not learned from it — a stronger promotion
   test than paired compression, because it is grounded in something Mike wrote
   rather than in a model's score.

# Sequencing

E-2007 replaces the flat file with a lessons table. Read against that if it
lands first; against the file if not. The content is what matters, not the
storage, and use 3 in particular should be specified against the CONTENT so it
survives the migration.

E-1721 is an epic for turning lessons into features and guide docs. This is not
that: it does not act on any individual lesson, it uses the whole set as
evidence about what Mike dislikes.
