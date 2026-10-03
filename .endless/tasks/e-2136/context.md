## The observed failure

Sessions call `task show`, see that an analysis or plan exists behind a pointer,
and proceed without fetching it. Observed repeatedly and confirmed by asking a
session directly, which reported it had skipped the content.

Why, per the session's own account: progress bias — once there is enough to
begin, it begins, and a pointer does not block; no evidence of necessity, since
a field named `analysis` reads as supporting material; skim behaviour, because a
line formatted like metadata is processed as metadata; and no consequence, so
nothing corrects it and the behaviour does not self-repair.

The root defect was making elision SIZE-based when the relevant property is
role-based. A large analysis should be inlined *because* it is large and
load-bearing, which is the opposite of what a size threshold produces.

## The invariant that follows

**Every wrong guess must cost tokens, never silent incorrectness.** Over-fetching
is the error to bias into; under-fetching produces confident wrong answers,
which are undetectable in review.

Corollary from the brief: no correct agent workflow requires passing a flag.
Flags are for humans and scripts.

## How these two commands behave today

`task show --agent` emits key=value lines with `analysis_chars`,
`context_chars` and `notes_chars` as pointers — the shape that produces the
skip. The flag was `--llm` until E-1504 renamed it.

`session status` has no agent view at all; it is one of the sixteen commands
carrying `--json` and nothing else. Worse for an agent, its per-viewer state is
glyph-only: ownership is `●` versus `⟳`, focus is `◼︎`, duplicate work is `◫`,
and a row owned elsewhere is omitted from the drawing entirely. A session
reading its own board cannot tell its work from another session's — which it
did, reporting another session's tasks as the user's to spawn.

## Where the design lives

The private briefs `brief-2028-09-12-agent-folio-format.md` (the container, at
general scope) and `brief-2028-09-12-agent-facing-output-endless.md` (what
Endless puts in it). Both are gitignored and absent from worktrees; read them in
the main checkout.

## History

E-2190 was the `session status` half and is superseded here, so the shape rules
are decided while being tested on the hardest list. E-1504 was filed as this
task's dependent, was narrowed to the flag surface alone — the rename,
`--format`, and the two missing `--json` flags — and has landed.
