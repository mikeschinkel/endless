## Why this is a question rather than a task

The Agent Folio Format brief lists a parse/extract tool as an open question and
argues against it on the agent read path: selecting a part by name requires
knowing which part is needed, which is the judgment agents make badly — the
same judgment the inline-everything decision removes. Its conclusion is that
agents should read the whole stream.

That leaves a narrower case unanswered: pipelines. A human or a script that
wants one part out of a folio has no way to get it, and `--json` is the
script-facing contract only for commands that have one.

## What already exists

E-2136 ships a reader, but it is test-only: it asserts the invariants golden
files cannot — that every `Length` matches its body, that every `@meta` offset
lands on a header block, and that `@meta`'s row count equals the part count. It
is not exported and not reachable from the CLI.

So the mechanism is written. The question is whether exposing it is worth a
command, and if so what it is for.

## What to settle

Whether the pipeline case is real or hypothetical; whether extraction belongs in
`endless` at all given the format is meant to be general rather than
Endless-specific; and whether a reader encourages exactly the part-picking the
format's own rationale says not to do.
