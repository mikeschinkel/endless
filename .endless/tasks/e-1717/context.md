validate-db (ValidateTasks, internal/events/validator.go) replays the committed ledger into a temp DB and compares to the live DB, but only walks projection->live (missing-from-live plus field drift); it deliberately skips live->projection ('Skip this for now').

So a live-DB row with no ledger create -- the WAL-gap class the E-1710 audit found in E-1223/1224 -- is invisible to validate-db.
