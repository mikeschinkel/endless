Raised in the E-1883 brainstorm (Mike, 2026-10-01). Per ED-1602 each project
gets its own database file under the machine's config dir, named by slug.

Uniqueness itself is already enforced on one machine: `projects.name` and
`projects.path` are both UNIQUE in schema.sql, so a second registration with
the same name fails on the constraint — with a raw integrity error, not a
refusal that names a fix.

The real problem is that one committed value does three jobs. `register`
writes `name` into `.endless/config.json`, which is committed, so every clone
and fork of a repo carries the same name. That name is today the machine-local
key, the would-be database filename, and the project stamp on every ledger
event. A second clone or a fork on one machine, or two unrelated projects that
happen to share a name, collide on all three at once.
