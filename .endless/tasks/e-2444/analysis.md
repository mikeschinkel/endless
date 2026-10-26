Answers given by the user while planning:

- Q1 (unforeseen items): B, a structured answer `question answer EQ-n --post-land` that marks the item and counts as approval. One user act per unforeseen item.
- Q2 (storage): E, separate post-land rows in a new table keyed to the reconciliation in force at the land. The user pointed out that a complete new reconciliation per post-land run (option A) duplicates every settled verdict in the ledger and in SQLite. Separate rows keep each attempt as its own record.
- Q3 (blocking): B, a `landed` task still blocks its dependents.
- Q4 (withdrawal): allowed, for the user, on a `landed` task.
- Q5 (naming): the new status is `landed`. The ⏚ action, which marks a task with a landing record, is relabeled "shipped", and "shipped" is redefined to mean landed at least once, extended to finalized (completed) research and brainstorm tasks. The existing `Shipped` status group ("reached the verification gate or passed it") was a misuse of the word; it becomes `Reported`, the name planitem already uses for a near-identical set.
- Terminology: `post_land` / `--post-land` everywhere, not `after_land`. The verdict is `deferred`.
- Leave rule: the task moves `landed → assumed` when every post-land item has a non-`deferred` verdict and every post-land discrepancy's question is settled, the same rule the pre-land gate uses. A failed attempt files a question and keeps the task `landed`.

Decided without asking:
- The mark is a field, not a new item kind, because a new kind would have to be accepted by every `kind == deliverable` check.
- The post-land record is a new event kind.
- `--record-only` lands follow the same rule as other lands.
- `landed` is frozen apart from its post-land items.
- `task active` includes `landed`.
- `landed` has its own status glyph, and its own session-status action ranked above ⏚.

Out of scope: a time-based fault for a task that stays `landed`; `deferred` on exclusion items; merging `assumed`/`confirmed` into one status with a flag (the user will pick it up later); creating a glossary.

Context: E-2428's P9 is the case that prompted this; ED-1636 settled the shape.
