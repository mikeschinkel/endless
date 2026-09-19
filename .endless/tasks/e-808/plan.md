# E-808: Refactor write path to make event logs authoritative

## What needs to change

Currently in `cmd/endless-event/main.go`, the order is:
1. Execute SQL mutation (events.Execute)
2. Marshal event to JSON
3. Write event to segment file

For events-authoritative, the order must be:
1. Pre-allocate task ID (for creates/imports only)
2. Marshal event to JSON (with the real entity ID)
3. Write event to segment file
4. Execute SQL mutation as a side effect

If the SQL fails after the event is written, the event is in the log and will be picked up by rebuild-db. The event log is the source of truth.

## Concurrency: BEGIN IMMEDIATE

Multiple Claude sessions can run `endless-event` concurrently. Use SQLite's `BEGIN IMMEDIATE` to serialize writers:

1. `BEGIN IMMEDIATE` (acquires write lock; other writers block)
2. `SELECT COALESCE(MAX(id), 0) + 1 FROM tasks` (pre-allocate ID, safe under lock)
3. Write event to segment file (with pre-allocated ID)
4. `INSERT INTO tasks (id, ...) VALUES (pre_allocated_id, ...)`
5. `COMMIT` (releases lock)

If event write (step 3) fails, `ROLLBACK` and ID is never used. WAL mode ensures readers aren't blocked.

## Changes required

### cmd/endless-event/main.go (runEmit function)
- Reorder: pre-allocate ID, build event, write segment, then execute SQL
- For task.created and task.imported: call PreAllocateTaskID() before marshaling
- For all other kinds: entity ID is already known, no pre-allocation needed

### internal/events/executor.go
- Add `PreAllocateTaskID()` function using `MAX(id) + 1`
- Change `Execute()` to use `BEGIN IMMEDIATE` via `db.ExecContext` or raw `BEGIN IMMEDIATE`
- The executor's INSERT for created/imported tasks must use the explicit pre-allocated ID

### What stays the same
- internal/events/writer.go (segment writer unchanged)
- src/endless/event_bridge.py (Python bridge unchanged)
- src/endless/task_cmd.py (mutation wiring unchanged)

## Verification
- `endless task add "test"` works (pre-allocates ID, event written first)
- Two concurrent `endless task add` calls don't produce duplicate IDs
- `endless-event validate-db` shows no mismatches for newly created tasks
- Event segment file has the correct entity ID (not "0")
