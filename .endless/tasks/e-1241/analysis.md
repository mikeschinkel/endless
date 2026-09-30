Rationale: soft deprecation lets agents keep using the old verb; hard refusal forces them to learn 'task claim'.

The existing exit-with-ClickException pattern fits cleanly.

Reverts the call to claim_item in task_start_deprecated; keeps the hidden=True so the verb stays out of --help.
