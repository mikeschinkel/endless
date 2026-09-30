src/endless/db.py:_migrate_v5 contains two UPDATE statements that swap source/target on rows where dep_type IN ('needs','blocks') and where dep_type='replaces'.

Result: data corruption — at any moment, ~half of rows are in their intended direction and ~half are inverted, depending on parity of past invocations.

Symptoms originally observed as inconsistent labels in 'task show' / 'task relations' / 'task detail' for the same task; the labels were correct, the underlying data was flapping.
