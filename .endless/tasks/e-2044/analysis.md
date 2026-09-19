# What the file is

`.endless/report-prompts.jsonl` is an optional overrides file introduced by
E-1771 (commit 23d35ffb) and modelled on `verbs.jsonl` (E-1268). It is read from
two places, project first:

    <project>/.endless/report-prompts.jsonl
    ~/.config/endless/report-prompts.jsonl

Nothing creates it. Neither exists on Mike's machine, and the instruction in use
is the one embedded in `src/endless/report_prompts.py`.

It recognises four names: `minimize`, `denylist`, `judge`, `generate`.

# Why it should go

**Nobody can use it for the thing it exists for.** `minimize` is the whole
instruction, not a tweak: 5307 characters over 75 lines, which must contain
`{prompt}`, `{context}`, `{draft}` and `{denylist}`. As one JSON line that is a
5458-character string with every newline escaped. It has never been written.

**And it would not work if it were.** Per E-2041, `task report` reads the
instruction from `minimizer_champions` and never consults this file, so a
`minimize` line has had no effect since the loop first promoted anything.

**Mike has objected to it twice.** On 2026-08-20 (session 7ffe2a27) he cut the
CLAUDE.md line that pointed at it. On 2026-08-22 (session 2a04f666) he named it
directly: "I nixed that."

**The premise it was built on is gone.** The layer existed so wording could be
tuned without a task and a land. E-1975 replaced that with a loop that tunes the
wording automatically, and `endless minimizer reseed` for taking a shipped fix.
The remaining need — pinning a hand-written instruction — is E-2041's subject and
does not need a JSONL file to satisfy.

# It is a MOVE, not a deletion

Mike's intent when he objected was that the functionality move into the
database, not that overriding disappear. E-1975 already did most of that without
naming it: `minimizer_variants` stores instructions and `minimizer_champions`
says which is in use, so "a hand-written instruction" is already expressible as a
row. What is missing is a way to put one there and pin it, which is what the file
was pretending to offer.

So the file goes, and something like `endless minimizer set --from <file>` takes
its place: read a plain multi-line file — not a JSON string — store it as a
variant, and point the champion at it. That fixes the format problem and the
E-2041 problem in the same move, since a pinned variant is what `task report`
already reads.

# Scope

Remove the file's resolution entirely: `_read_layer`, `_machine_path`,
`_project_path` and `load_prompts`'s layering in `src/endless/report_prompts.py`,
plus the tests that cover them and any doc that mentions the file.

`denylist`, `judge` and `generate` currently resolve through the same layer and
do work. Decide per name whether anything is lost by fixing them to the embedded
default. `denylist` is the one with a plausible claim, since ED-1557's design
anticipated appending an offending phrase to it.

# Consequence for E-2041

E-2041 asks that a hand-written `minimize` line be honoured. If this file is
removed, that task is answered by removal rather than by implementation, and
should be superseded rather than worked.


