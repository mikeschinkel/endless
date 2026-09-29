As shipped (implementation choices the analysis left open):

- `task supersede <old> --by <new>` is the command; `task replace` stays a WORKING hidden alias (a copy of the command object with hidden=True, so `supersede` stays listed and `replace` does not).
- `--replaces` on task add / task update / epic add is renamed `--supersedes`, and the old flag is RETIRED with the project's existing `retired_option` (refused by name, pointing at the new flag), not aliased. That follows E-1000's rule for renamed flags; only the command keeps a working alias, as the analysis asked.
- `task link --type replaces|replaced_by` is not aliased: the refusal already lists the valid names.
- Stored dep_type: goose migration 00009 renames existing 'replaces' rows to 'supersedes' (UPDATE OR IGNORE + DELETE, so a pair holding both folds instead of aborting).
- Ledger replay: internal/events resolves every task_dep payload through canonicalDepType ('replaces' -> 'supersedes') on both the live executor and the replay handler, for create and delete alike.
- Display: labels Supersedes / Superseded by, the '(superseded by E-N)' note, the JSON `superseded_by` key, the agent `superseded_by=` token, the authority banner, and Go session status (field, SQL column, note, JSON key).
- Docs: guide tasks.md and index.md; transitions.go's comment that said the relation and status vocabularies differ is rewritten, since they now match.
- Test files renamed to match: test_superseded_by_inline.py, session_status_superseded_test.go, superseded_test.go.
- Left alone on purpose: db.py's v5 migration (historical), decision/research docs, LESSONS.md.
