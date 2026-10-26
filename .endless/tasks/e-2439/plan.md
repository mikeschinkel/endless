# Open questions

Terms used below:

- **post-land item** — a plan item whose work can only be done after the task has landed: for example, a row written into a table that the task's own migration creates, which the main database gets only on land + install.
- **reconciliation** — what `endless task reconcile` records: one verdict per active plan item. Today it is refused once a task is `unlanded`, `assumed`, `confirmed` or `completed`.
- **deferral** — the record, made before the land, that an item will be done after it.

## Q1. Where is an item's post-land nature recorded?

| Option | Pros | Cons |
|---|---|---|
| A. In the plan: a deliverable item carries a post-land mark, set while planning and approved with the plan. The pre-land reconciliation gives it a verdict such as `after_land`, which files no question. | The user approves the deferral with the plan, so no question round-trip at reconcile. The system knows which items it is still waiting on without reading free text. | Only covers items someone foresaw while planning. E-2428's P9 was in the plan, but nobody marked it post-land. |
| B. At reconcile time: the item is reconciled `blocked`, and the user's answer to its question is a structured choice ("do it after land") instead of free text. | Covers items nobody foresaw. Planning and approval stay as they are. | Every post-land item costs a question and an answer, even an obvious one. |
| C. Both: A for items foreseen while planning, B for the rest. | Covers foreseen and unforeseen items. A foreseen item skips the question. | Two ways into the same state, and the task view has to render both. |

Recommendation: C. Migrations that repair data are common enough that skipping the question for them pays off, and P9 shows that some items won't be foreseen.

## Q2. What records the verdict after the land?

| Option | Pros | Cons |
|---|---|---|
| A. `endless task reconcile` accepts a landed task, but only for its deferred items, in a file holding just those entries. Each run records a new reconciliation. | One command and one file format for every verdict. The reconciliation history shows the post-land verdict in sequence. | reconcile's rule that every active item has exactly one entry needs a partial-file exception for landed tasks. |
| B. A new single-item verb, e.g. `endless task plan done <task> <item> --note "<evidence>"`. | One line per item; no file to fill in. | A second place that writes verdicts, so "where do verdicts come from" has two answers. |

Recommendation: A. A verdict is a verdict, and keeping one writer keeps one history.

## Q3. Who may record a post-land verdict?

| Option | Pros | Cons |
|---|---|---|
| A. The agent, attributed to its session, as with reconcile today. | No extra step for the user. Same rule as pre-land reconciliation. | After the land nothing re-checks the work: no verify run, no further gate. An agent's word is the only evidence that the item was done. |
| B. The agent records it, and that files a question for the user to confirm. | The user sees and accepts every verdict the land and verify did not cover. | An extra answer per item, for work the user often watched happen in the same session. |
| C. The user only. | The strongest guarantee. | The user has to write verdict and evidence themselves, for work the agent did. |

Recommendation: A. The deferral itself was the user's call (Q1), and the attribution and evidence note are in the record.

## Q4. What evidence must a post-land verdict carry?

| Option | Pros | Cons |
|---|---|---|
| A. A note, as with every verdict today. | Works for machine-local state, which verify runs cannot reach: they get a temp HOME and a private tmux server by design. | Nothing checks the note. |
| B. A command whose output is recorded with the verdict, e.g. `endless task show E-2206 --db main`, run by the recording command itself. | The evidence is captured, not paraphrased. | The command runs against real state outside verify's isolation, so whatever it does is unguarded. A command that writes, as well as reads, would run as a side effect of recording. |

Recommendation: A, with the item's own `verify:` line naming the check, as plan items already do.

## Q5. What does an open deferred item do to the task's status?

| Option | Pros | Cons |
|---|---|---|
| A. Nothing: the task lands to `assumed` as now, and the item shows as open in `task show` and the task list. | The land stays unblocked, which is the point of deferring. | A user can `confirm` a task with the item still undone. |
| B. `task confirm` (and `complete`) refuse while a deferred item has no verdict; `assumed` is still allowed. | `confirmed` keeps meaning every item is accounted for. | The user can't confirm a task while waiting on post-land work that is not theirs to do. |
| C. A as default, and a deferred item still open after a set time raises a fault. | Undone post-land work surfaces without the user having to look. | A time threshold to choose, and a fault for items that are legitimately slow. |

Recommendation: B. `confirmed` is the user's statement that the work is done, and an undone item contradicts it.
