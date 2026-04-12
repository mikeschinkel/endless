#!/usr/bin/env bash
# db.sh — SQLite database helpers for Endless

# Initialize the database schema (idempotent)
db_init() {
    [[ -f "$ENDLESS_DB" ]] && return 0
    ensure_config_dir
    sqlite3 "$ENDLESS_DB" < "$ENDLESS_ROOT/sql/schema.sql" > /dev/null
}

# Execute a write statement (INSERT/UPDATE/DELETE)
# Usage: db_exec "INSERT INTO projects ..."
db_exec() {
    db_init
    sqlite3 "$ENDLESS_DB" "$1"
}

# Run a query and return results
# Usage: db_query "SELECT * FROM projects"
# Options via environment:
#   DB_MODE="-json"    → JSON output
#   DB_MODE="-csv"     → CSV output
#   DB_MODE="-column"  → Column output (default)
db_query() {
    db_init
    local mode="${DB_MODE:--column}"
    sqlite3 "$mode" -header "$ENDLESS_DB" "$1"
}

# Run a query returning a single value (no headers, no formatting)
# Usage: val=$(db_scalar "SELECT count(*) FROM projects")
db_scalar() {
    db_init
    sqlite3 "$ENDLESS_DB" "$1"
}

# Check if a row exists
# Usage: if db_exists "SELECT 1 FROM projects WHERE path='...'"; then ...
db_exists() {
    db_init
    local result
    result=$(sqlite3 "$ENDLESS_DB" "$1")
    [[ -n "$result" ]]
}
