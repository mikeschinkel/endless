-- E-2189: rename the task_deps dep_type 'replaces' to 'supersedes'.
--
-- The relation `task supersede <old> --by <new>` records now shares one
-- vocabulary with the `superseded` status it sets and with `decision
-- supersede`, whose decision_relations row has been 'supersedes' since E-1920.
-- Only the stored name changes: the row stays active-voice (source=new,
-- target=old), so no source/target swap.
--
-- The ledger is NOT rewritten. It is immutable, and its task_dep events still
-- say 'replaces'; internal/events resolves every task_dep payload through
-- canonicalDepType, so a full rebuild projects those events straight onto
-- 'supersedes' rows and this step then matches nothing.
--
-- OR IGNORE plus the DELETE is for a pair already holding a 'supersedes' row
-- beside its 'replaces' one — impossible through the CLI before this step, but
-- the UNIQUE constraint includes dep_type, and a plain UPDATE would abort the
-- whole migration on such a pair rather than fold the duplicate away.
--
-- Data only: no DDL changes, so TestMigrate_MatchesSchemaSQL is unaffected.
--
-- No Down section: goose never rolls this set back in production.

-- +goose Up

UPDATE OR IGNORE task_deps SET dep_type = 'supersedes' WHERE dep_type = 'replaces';
DELETE FROM task_deps WHERE dep_type = 'replaces';
