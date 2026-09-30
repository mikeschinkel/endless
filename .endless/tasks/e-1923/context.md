Cause: src/endless/task_cmd.py re-declares its own hardcoded status tuple that omits submitted, instead of using the canonical TASK_STATUSES at src/endless/cli.py (which includes it and backs every Click choice).

Effect: a task cannot be moved back to submitted at all, so an accidental status change is unrecoverable via the CLI.
