## Observed

    endless task show E-1347 --json --db main

returns, in the same object:

    "analysis": null
    "analysis_chars": 6503

The field has 6503 characters. The machine format says it is null.

`--analysis --json` (or `--all-fields --json`) returns the body. So the display
flags that gate the HUMAN renderer are also gating the JSON payload.

## Why this is worse than the terminal case

The terminal renderer is honest about hiding: it prints
`Analysis: 6503 chars (--analysis to display)`, which tells a reader both that
content exists and how to get it. That placeholder is the entire reason a human
can recover.

`--json` has no placeholder. `null` is the same value the field carries when it
is genuinely empty, so a consumer cannot distinguish "no analysis" from
"analysis withheld pending a flag you did not pass". The `_chars` sibling
contradicts it, but only for a consumer who already suspects the problem — which
is exactly the consumer who does not need the warning.

A machine format that silently omits populated content is not a rendering
choice; it is wrong output. Display flags belong to the display.

## Inconsistency in the sibling keys, same call

    description       557 chars returned    description_chars: null
    analysis          null                  analysis_chars: 6503
    text              null                  text_chars: null
    outcome           null                  outcome_chars: null

`description` is always returned and its `_chars` is null. `analysis` is
withheld and its `_chars` is populated. So neither the body nor the count can be
relied on to indicate presence, and the two disagree about which is
authoritative. Under `--all-fields --json` the same task reports `text` as an
empty string and `outcome` as null — two spellings of absent, in one object.

## Direction

`--json` should carry every field's real value, always: content when there is
content, and one consistent spelling of absent when there is none. The display
flags (`--analysis`, `--text`, `--outcome`, `--all-fields`, `--no-description`)
should affect only the human renderer.

If a caller genuinely needs a lighter payload, that is a separate, explicit
opt-out — and it should still be distinguishable from empty. Note that `--llm`
is a second machine-facing format and needs the same audit; it was not measured
here beyond confirming it is JSON-shaped.

Decide what a consumer should see, then make the `_chars` keys agree with it —
they are either redundant (drop them) or they are the presence signal (populate
them uniformly). Today they are neither.

## Blast radius

Any agent or tool reading task content through `--json` sees empty fields for
populated ones and proceeds on a partial record. This is the machine-facing
instance of the failure that produced the `--text` to `--all-fields` sweep: the
design content of a task sitting behind a flag the reader did not know to pass.
There the reader was a human who could at least see the placeholder.
