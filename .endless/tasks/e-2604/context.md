Endless commits `.endless/` into each project's repository, and the project
rule is never to gitignore it, because the event ledger is the durable record
the database is rebuilt from. For a public repository, that rule means
publishing every task's text, the decisions, the lessons, and actor ids that
name the user and their machine.

To settle: what a public repository should publish, and how Endless keeps
the rest. Directions so far, none chosen:
- a per-project setting that marks a repository public and changes what is
  committed;
- keeping the ledger on a branch or in a repository that is not pushed with
  the code;
- redacting or pseudonymising actor ids before they are written;
- telling the user at setup, plainly, what will be published.

The inventory research task sibling to this one supplies the facts. The
"never gitignore `.endless/`" rule is in scope for change.
