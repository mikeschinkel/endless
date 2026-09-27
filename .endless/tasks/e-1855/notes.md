## Justification

Suspected data-loss bug: user reports a sequence of session goto/session resume commands ended with the sessions row for the task gone/overwritten (active_task_id no longer matches). Requires forensic ledger + code-path analysis across multiple CLI commands before any fix can be scoped; not a single inline do-task.
