## Inherited from E-2217 (landed 2026-10-18)

E-2217 shipped the project identity (root commit, in config.json and
projects.identity) and the machine-local `projects.slug` (= name by default,
kept across a rename, never committed). Per Mike's option 1 it REFUSES a name
or slug clash rather than prompting, because one machine database cannot hold
two projects of one name. Two items move here:
- prompt for a different slug at registration when the name clashes, once
  per-project databases make two same-named projects possible;
- name each per-project database file `projects/<slug>.db` from projects.slug.
