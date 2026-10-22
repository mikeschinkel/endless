# Apply schema migrations to every database file

E-2215 child 5.

## Work

- Split the goose migration set into machine migrations and project
  migrations. Each file keeps its own `goose_db_version`.
- Apply project migrations to every project file under ConfigDir() and its
  self_dev sandbox counterpart. Apply machine migrations to the machine file.
- Split `schema.sql` along the same line, or generate it per file if E-2021
  lands first. Keep `TestMigrate_MatchesSchemaSQL` passing for each file.
- Coordinate with E-2034 (versioned migrations instead of apply-on-connect)
  and E-2021 (schema.sql generated from goose).
