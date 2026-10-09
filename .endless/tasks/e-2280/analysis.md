## Settled with Mike in the brainstorm

- **When.** The agent runs the work to completion and does not stop
  mid-implementation for a deviation. While usage is subscription-based,
  Mike is answering other tasks in parallel anyway. Revisit this if Endless
  moves to API-key usage.
- **Reconciliation.** After implementing and before handing over a verify,
  the agent reconciles its work against the plan item by item: done as
  planned, done differently, not done, or blocked. Work outside the plan is
  listed as extra. "No discrepancies" is recorded affirmatively, never
  inferred from silence.
- **Kinds, ordered most to least concerning:** doing less than the plan;
  something it could not do (including being blocked by a hook); a different
  route to the same goal; doing more than the plan. All four are raised.
- **Carrying them.** The reconciliation is the record. Each discrepancy in
  it becomes an open question tagged with its kind, so questions sort in that
  order and plug into the existing open-question machinery.
- **Gate.** One shared check, called by the agent's hand-over (setting
  `unverified`, the verify request from the verify/land rework, and any future
  auto-verify trigger) and by the verify command itself as a backstop. It
  refuses unless a reconciliation exists, every discrepancy question is
  answered or closed, and nothing outside `.endless/` has changed on the
  task branch since the commit the reconciliation recorded. Endless's own
  ledger commits therefore do not make it stale, while any code change after
  a fix forces a fresh reconciliation.
- **Scope.** The todo, bugfix and docs types. Research and brainstorm deliver
  an outcome, not code checked against a plan.
- **Plans must be itemizable.** Plan items become structured rows with IDs,
  so the gate can deterministically check that every item has a
  reconciliation entry. A text check for "numbered items" was rejected: it
  tests formatting, not itemization, and gives false positives and false
  negatives. Mike does not want gates that are not fully deterministic.
- **Sequencing.** This task is blocked on the content-field research and
  builds on the plan-item model it proposes. Mike chose this over shipping a
  text-based reconciliation first, to avoid building something that would
  soon be replaced.
- **One task.** Plan shape, guidance and prompting, handoff wording, and the
  record and gate share one cause and ship together (per the decision that
  agents close more than they file).

## Expected plan items, to be confirmed against the research

1. Plan items stored as rows, as the research specifies.
2. Guide: the `plan` field's definition requires discrete, independently
   checkable items, with rationale moved to `analysis`.
3. Plan-time prompting: `/needs-plan`, `task prime` and the handoff's planning
   instructions ask for itemized plans. Folding `/needs-plan` into Endless is
   a later step, not this task.
4. Handoff and guide wording: replace the post-hoc "say so in your reply"
   reporting, including the discovery bullet's "do it", with "it goes into the
   reconciliation".
5. The reconciliation record and the command that writes it, with each
   discrepancy filed as a kind-tagged question.
6. The shared gate in the hand-over paths and in verify, including the check
   against the recorded commit.
7. After implementation: draft the decision recording the rule. Mike asked
   that it be drafted last, not up front.
