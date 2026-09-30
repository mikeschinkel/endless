parse each line as a granular subcommand prefixed with endless project next, dispatch through normal handlers inside one BEGIN IMMEDIATE TRANSACTION.

Each line emits its own event in project_next_events sharing a generated batch_id.

Fail-atomic.

Lines parsed with shell-style quoting.

Reuses BEGIN IMMEDIATE / validation scaffolding from prior revise work.
