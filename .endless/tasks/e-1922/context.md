monitor.DB() executes its embedded schema.SQL on every owned connection with no check that the DB has the change files that schema assumes.

Nothing connects a binary's expectations to the DB's actual state, so a binary installed before its change file is applied can create schema objects referencing columns that do not exist —

E-1917's trigger reads NEW.changed_by_session and SQLite resolves trigger bodies at fire time, so every UPDATE tasks would fail.

Today that is avoided only by convention (land, which runs apply-change, before just install).
