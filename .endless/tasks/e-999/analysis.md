The tasks.text column is conventionally treated as 'the plan' (see comments in cmd/endless-hook/claude.go referencing --text <plan-file>; E-971's planned plan-bearing detector keys off plan presence on tasks). But there is no field for problem analysis, design briefs, research notes, decision rationale, or postmortems — content that is long-form but is not a plan.

Today the hook does not read tasks.text for any behavioral decision (GetActiveTasks selects only description), so the overloading is not yet causing concrete misbehavior. However, the moment E-971's plan-bearing detection lands, anything stored in text will be interpreted as 'this task is planned' — which would mark mere analysis as planned and bypass actual planning.

Decide: how should non-plan long-form content live on a task?

Options:
1. Rename text -> plan AND add a separate analysis field (or notes / brief). Cleanest semantics; one field, one purpose. Cost: schema migration + CLI churn.
2. Rename text -> plan AND keep description as the home for everything between 'one paragraph' and 'a full plan'. Cost: description becomes unbounded; task show summaries get noisy (the original problem we hit on E-989).
3. Keep text as is but document the convention 'text = plan, full stop' and refuse to write analysis there. Cost: discipline-only; no enforcement.

Recommendation pending: option 1. Tradeoff: schema migration cost up front, paying off as soon as plan-bearing detection lands.

This is the design decision; the rename itself is filed separately so they can land in order. Origin: E-989 finalization revealed the gap.