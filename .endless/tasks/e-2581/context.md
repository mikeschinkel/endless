The task that pinned correct's undeclared-reference list to the schema
scoped its test's naming rule to session references, which is where the
six missing entries were. Measured against the live schema with the same
query, a rule over the word "task" surfaces four INTEGER columns that hold
a tasks id with no FOREIGN KEY and are not on the list either:

    error_triage.fix_task_id
    errors.task_id
    errors_sources.task_id
    rater_claims.task_id

Adding them is not the one-line change the six session rows were, because
the list has a live reader for tasks. The delete refusal counts referrers
for a correcting delete of a task, so a task that an errors, error_triage
or rater_claims row names would be refused — and none of those tables is a
correction target, so the user could never clear the referrer and the task
could not be removed through correct at all. Today the refusal names only
referrers the user can correct.

So two things are open: whether a referrer in a table correct cannot touch
should count toward a task delete at all (a task soft-removes, keeping its
id, so a dangling errors.task_id still names the right removed row), and,
once that is settled, widening the schema test's rule to "task" and adding
the four rows so the list maps every undeclared reference the schema holds.

The rule, the test and the list are in internal/events: the naming rules
are a table in correction_refs_test.go, so widening is one row plus the
list entries once the referrer question is answered.
