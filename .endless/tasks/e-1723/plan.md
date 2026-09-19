# E-1723 (brainstorm) — Decide how to address the MEMORY.md entries E-1722 flagged

**Un-obsoleted and repurposed.** Working session over E-1722's triage table: for
each (a)-feature and (b)-guide candidate, decide the actual disposition and rough
shape, then file the implementation follow-ups. Blocked by E-1722 (needs its
findings).

## Input

E-1722's triage table (entry · category · feasibility · priority).

## Per-candidate decisions (with Mike)

For each (a)/(b) candidate:

- **Disposition:** convert to feature / convert to guide / actually leave in
  memory / drop as obsolete. (Triage proposes a fit; here we commit.)
- **Rough shape:** for a feature — which surface (hook gate / CLI check / schema
  enum) and the gate-vs-guardrail framing (`gates-not-guardrails`). For a guide
  doc — which section (`docs/guide/*.md`) and whether the cross-reference /
  `--help` map needs regenerating.
- **Priority.**

## Filing follow-ups (judgment — per Mike, not one-per-candidate)

- File a full task where it earns its keep (`--cleans-up E-1721`).
- Combine related lessons that share a surface into one task.
- Fold into an already-filed task where one exists — **search the ledger first**
  (`search-before-filing`).
- For (d) obsolete entries, recommend pruning from the memory store; Mike decides.

## Deliverable

Decisions recorded per candidate + the filed (or folded) implementation
follow-ups. E-1725 (`(auto)` display) already exists — reuse, don't duplicate.

## Cross-reference

Defer index-size / navigability lessons to E-1724's maintenance model rather than
converting them per-lesson here.

## Scope guard

Endless-scoped; generic multi-project tooling deferred.
