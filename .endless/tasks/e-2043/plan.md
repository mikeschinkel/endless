# E-2043 — observe-only minimizer mode

## Problem

`minimizer.enabled` is boolean. Off means the whole report channel is off: the
agent is not told to use `task report`, the Stop hook does not enforce it, and
no draft is recorded. So switching the minimizer off to stop it damaging replies
also stops the corpus growing — during exactly the period when accumulating one
is most valuable, because that corpus is what a later re-enable would be
calibrated against.

## Approach

Add a third state to the existing switch rather than a fourth config key.
`project_minimizer_config` (`src/endless/config.py`) already normalizes three
spellings into `{enabled, optimizer}`; add `"minimizer": {"mode": "observe"}`
alongside the booleans, normalizing to a third field rather than overloading
`enabled` — a boolean that has three meanings is how the E-1953/E-1975 rename
confusion started.

Resolved shape: `{"enabled": bool, "optimizer": bool, "observe": bool}`.
`observe` is meaningful only when `enabled` is false; when both are false the
channel is fully off, as today.

In observe mode:

- The agent IS instructed to run `task report --draft-file` (guide + handoff
  text keyed off the same config read that already omits them when off).
- `report_cmd` records the corpus row — `user_prompt`, `raw_draft`,
  `fetched_context`, `variant_hash`, `task_type` — and runs the fetch policy,
  since replay needs the context record.
- It does NOT call the minimizer. No model call, no `sanctioned_text`.
- It echoes the draft back unchanged, so the agent's own reply is what ships.
- The Stop hook does not compare the final message to anything.

`sanctioned_text` NULL is the marker for "recorded, never minimized". Corpus
selection in `minimizer_store` already requires `sanctioned_text IS NOT NULL`,
so observe rows are excluded from replay automatically — no filter change, and
they still serve as (prompt, draft, context) triples a future champion can be
replayed against once one exists.

## Go mirror

`monitor.readMinimizer` must learn the same third state or the Go hook and the
Python CLI will disagree about whether to enforce. Same closed set of spellings.

## Verification

- Config with `{"mode": "observe"}` resolves to `enabled=false, observe=true` in
  both Python and Go, and the two agree on every spelling in the existing table.
- A report in observe mode writes a `session_gates` row with `raw_draft` and
  `fetched_context` set and `sanctioned_text` NULL, makes no model call, and
  prints the draft byte-for-byte.
- The Stop hook accepts a final message that differs from anything.
- `minimizer_store.corpus(...)` does not return observe rows.
- Fully-off remains fully off: no instruction, no row, no enforcement.

## Open question

Whether observe mode should still run the JUDGE (which scores fidelity of a
minimization) — it cannot, since there is no minimization to score. So observe
rows carry no judgment and `evidence()` sees nothing from them. That is correct
but worth stating: observe builds replay material, not supervision signal. Only
labels and A/B picks supply supervision, and both need the channel on.
