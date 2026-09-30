Every task mutation already emits an event with an `actor` field, but `actor.id` today is `$USER@$HOSTNAME` — sessions running as the same user cannot be distinguished.
