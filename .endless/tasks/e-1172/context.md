endless-sandbox destroy <name> currently exits 1 with a stderr message when <name> doesn't exist — correct default for interactive use, but inconvenient for scripts that want idempotent cleanup.

Discovered 2026-05-03 verifying E-1154.
