Use case: we had to use raw SQL to reparent 50 children of E-444 to root when we dissolved that container task.

The existing 'task update <id> --parent' handles single items but no batch mode.
