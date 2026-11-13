Endless commits a project's `.endless/` directory into the project's own git
repository: the event ledger, per-task copies of every content field, the
decisions, and `LESSONS.md`. When that repository is public, pushing it
publishes all of that, and nothing tells the user it will.

What prompted this: a stranger emailed Mike after an agent of theirs read a
lesson in a public repo's `.endless/LESSONS.md`, quoting the lesson and a
decision id back to him. The material was meant as working notes, and it was
read as public content.

The text in those files is mostly written by agents, which paste logs, error
output, configuration and command output into task fields. Ledger entries
also carry actor ids in the form `user@hostname`, plus session ids. A secret
that reaches any of these is published on the next push, and deleting it
later does not remove it from git history.

A gitleaks scan (values redacted) over the full history of every public repo
that commits `.endless/` found nothing under `.endless/` on 2026-11-12. So
this is about risk, not a known leak.

This holds for any Endless user with a public repository, not only this
machine.
