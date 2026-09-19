# Terms, before anything else

**The instruction.** When `endless task report` shortens an agent's reply, it
calls a model and hands it two things: the agent's full draft, and a block of
text telling the model what to cut. That block is the instruction.

**The overrides file.** `.endless/report-prompts.jsonl` holds one JSON object
per line, each with a `name` and a `text`. The line whose `name` is `"minimize"`
supplies a hand-written instruction. There are three other recognised names —
`denylist`, `judge`, `generate`.

**The two tables.** `minimizer_variants` stores instructions, each keyed by a
hash of its own text. `minimizer_champions` holds the hash of the one in use.

**The background job.** `minimizer-loop`. Every ten minutes it scores shortenings
that already happened. Less often it asks a model to write a new instruction,
tries it against the one in use over drafts already stored, and rewrites
`minimizer_champions` to point at the new one if it did better.

# The defect

`endless task report` reads the instruction from `minimizer_champions`. It never
reads the `minimize` line. So a hand-written instruction has no effect from the
first promotion onward, and nothing says so.

The other three names still work: `denylist`, `judge` and `generate` are read
from the file on every call.

Demonstrated:

    override text present in what the model gets: False
    denylist override path still splices: True

`src/endless/report_prompts.py` states the opposite in its own docstring —
"Editing an OVERRIDE needs no task and no land. That is the whole point of the
layer." That sentence is true for three of the four names and false for the one
that matters.

A hand-edited instruction does take effect after `endless minimizer reseed`,
which rebuilds the champion from the shipped defaults plus the overrides file.
Nothing points that out, and it is a strange thing to have to know.

# Why the fix is not one line

Every shortening Endless stores also stores, in `session_gates.variant_hash`,
the hash of the instruction that produced it. If `task report` used the
hand-written instruction but still wrote the champion's hash there, that column
would name an instruction which did not produce that reply.

The background job reads those rows to decide whether the instruction in use is
any good. It would be scoring the champion using replies the hand-written
instruction produced — and promoting or rejecting on that basis. Fixing the
visible problem would quietly corrupt the evidence the job learns from.

# Proposed fix

When the overrides file has a `minimize` line:

  1. insert that text into `minimizer_variants`, which gives it its own hash;
  2. use it for the shortening;
  3. write THAT hash into `session_gates.variant_hash`.

It does not become the champion. `minimizer_champions` keeps pointing where it
pointed, so removing the line returns to the job's instruction with nothing lost.

# Open question for Mike

While a hand-written instruction is in force, should the background job keep
promoting new ones underneath it? Promotions would be invisible in the replies
and would take effect the moment the line is deleted. Stopping the job instead
means an override silently freezes learning. Either is defensible; the choice
should be explicit, and `endless minimizer status` should say which is happening.
