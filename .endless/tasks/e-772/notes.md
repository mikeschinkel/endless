Added safety net at end of migrateV2 in both Go and Python: if sessions table doesn't exist after all migration steps, create it fresh.
