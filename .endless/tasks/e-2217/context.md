Raised in the E-1883 brainstorm (Mike, 2026-10-01). The per-project database
is named by project slug, so two registered projects with the same slug —
two clones or forks of one repo side by side — would share one database file.

Endless has been silently depending on project slugs being unique on a
machine; it happens to hold across Mike's ~60 projects, but nothing enforces
it, and other users will register duplicates.
