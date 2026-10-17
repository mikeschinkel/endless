On a project directory that isn't a git work tree, task add wrote task.created lines to the project's .endless/db-ledger/ and then failed to commit, leaving uncommitted lines that claimed task IDs. Those IDs were then reused by tasks created in other projects.

Check that the commit can succeed before writing the line, or remove the line when the commit fails. Projects with no remote, and with no commits yet, already work once git init has run.
