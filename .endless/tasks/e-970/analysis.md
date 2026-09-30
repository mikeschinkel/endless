Project-scoped with global fallback.

New CLI: 'endless phrase {add,list,disable,enable,remove}' with --match and --case-sensitive flags. Seeds: pivot triggers (actually, wait, by the way, ..., plus PIVOT case-sensitive); verbs from current _TITLE_VERBS in src/endless/task_cmd.py; actions from hardcoded regexes in cmd/endless-hook/claude.go. Naming convention: kebab-case for kind values (user-typed at CLI), snake_case for column names. Plan: .endless/plans/E-970.md.
