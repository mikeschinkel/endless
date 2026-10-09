## Synthesis

**The problem, restated.** The failure is not that agents implement
deviations. It is that Mike starts a verify and then learns it was premature,
because work still needs changing. Agents mostly do not notice a deviation
while working. A different route feels like ordinary engineering, and only
looks like a deviation when the diff is read against the plan. So the rule is
not "raise it before implementing" but "reconcile before any verify is handed
over". The task was retitled to match.

**Why it happens today.** The only place a handoff asks the agent to report
anything is its final message, after the work is done. The discovery guidance
("do it, note it, say so in your reply") explicitly asks for scope growth to
be reported after the fact. "STOP and ask" covers ambiguity, not a choice the
agent thinks is obvious.

**Kinds of discrepancy** (from E-2225; all four are raised, ordered most to
least concerning):
1. Doing less than the plan.
2. Something it could not do, including being blocked by a hook.
3. A different route to the same goal.
4. Doing more than the plan.

**The rule as settled:**
- The agent runs the work to completion and does not stop mid-implementation.
  This holds while usage is subscription-based and Mike answers other tasks in
  parallel. Revisit it if Endless moves to API-key usage.
- After implementing, the agent reconciles item by item against the plan:
  done as planned, done differently, not done, or blocked, plus extra work
  outside the plan. "No discrepancies" is recorded affirmatively.
- Hybrid carrier: the reconciliation is the record, and each discrepancy
  becomes an open question tagged with its kind, sorted in the order above.
- One shared check, called from every hand-over path (setting `unverified`,
  the verify request, any future auto-verify) and from the verify command
  itself, refuses unless all three of these hold:
  - a reconciliation exists;
  - every discrepancy question is answered or closed;
  - nothing outside `.endless/` has changed on the branch since the commit
    the reconciliation recorded.
  After a fix, the agent therefore has to reconcile again.
- Scope: the todo, bugfix and docs types.
- Plans become itemizable as structured rows with IDs, so the gate can check
  deterministically that every item has an entry. A text check for "numbered
  items" was rejected: it tests formatting rather than itemization, and Mike
  wants no gates that can return false positives or false negatives.
- `/needs-plan` is the plan-time prompting vehicle for now. Folding it into
  Endless commands or hooks is a later step.

**Sequencing (Mike's choice).** Structure first: the implementation is
blocked on research into what agents put into the content fields, so it
builds on the resulting plan-item model rather than on an interim text format
that would soon be replaced. The decision recording the rule is drafted last,
after implementation, not now.

**One implementation task, not three.** Plan shape, guidance and prompting,
handoff wording, and the record and gate share one cause, so they ship
together (ED-1550).

## Follow-ups

- **E-2279** (research): investigate what agents put into plan, context,
  analysis and outcome, and propose a content model with plan items as rows.
  It blocks E-2280.
- **E-2280** (todo, blocked by E-2279): reconcile finished work against
  itemized plans before verify. Its analysis holds the settled requirements
  and the expected plan items, including drafting the decision last.
