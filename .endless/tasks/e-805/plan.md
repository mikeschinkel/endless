# E-805: Implement Go Event-Writing Binary with Dual-Write

## Context

E-805 is Stage 1 of the event-sourcing migration. It introduces `endless-event`, a Go binary that is the **single writer** for both event segment files and SQLite. Python stops calling `db.execute()` for task mutations and shells out to Go instead. This eliminates dual-write corruption risk and starts the Go migration naturally.

Scoped to **task mutations only** (7 event kinds). Other entity types expand in follow-up tasks.

## Decisions

1. **New `endless-event` binary** in `cmd/endless-event/`. (Task to create unified `endless-go` binary later.)
2. **CLI flags** for envelope fields, `--payload` as JSON string.
3. **Go does both DB write + event write** (Option C). Single writer, no partial state.
4. **Python calls Go** at each mutation function in `task_cmd.py`.
5. **Task mutations only** (7 kinds) for this task.
6. **Count-based segment rotation** at 10,000 events.
7. **Node ID passed from Python** via `--node-id` flag. Python reads/creates in config.json.
8. **O_APPEND** for concurrent safety. **FAIL LOUDLY** (exit non-zero with error) if any JSONL line exceeds 4KB.

## Architecture

```
Python CLI (task_cmd.py)
  │
  │  subprocess.run(["endless-event", "emit", "--kind", "task.created", ...])
  │
  ▼
endless-event binary (Go)
  ├── Parse flags + validate
  ├── Generate kairos timestamp
  ├── Write event to .endless/events/events-{node}-{seq}.jsonl (O_APPEND)
  ├── Execute SQL mutation against ~/.config/endless/endless.db
  └── Print event JSON to stdout (for Python to log/verify)
```

## Files to Create

### Go

```
cmd/endless-event/main.go        -- Binary entry point, flag parsing, dispatch
internal/events/writer.go        -- Segment file writer (append, rotate, 4KB check)
internal/events/writer_test.go   -- Writer tests (append, rotation, 4KB enforcement)
internal/events/executor.go      -- SQL executor for task mutations
internal/events/executor_test.go -- Executor tests
```

### Python changes

```
src/endless/task_cmd.py          -- Replace db.execute() calls with subprocess to endless-event
src/endless/event_bridge.py      -- Helper: _emit_event() shells out to endless-event
```

### Build changes

```
justfile                         -- Add endless-event to build/install targets
```

## Go Implementation

### cmd/endless-event/main.go

Entry point. Parses subcommand `emit` with these flags:

```
endless-event emit \
  --kind task.created \
  --project endless \
  --entity-type task \
  --entity-id 804 \
  --actor-kind cli \
  --actor-id "mike@macbook" \
  --node-id a7f3 \
  --project-root /Users/mike/Projects/endless \
  --payload '{"title":"Implement kairos","phase":"now","status":"needs_plan","type":"task","sort_order":10}'
```

Flow:
1. Parse and validate all flags
2. Validate `--kind` is in `events.ValidKinds`
3. Parse `--node-id` via `kairos.ParseNodeID()`
4. Create `kairos.Clock`, call `clock.Now()` for the timestamp
5. Construct `events.Event` envelope
6. Call `event.Validate()`
7. Marshal event to JSON; check length < 4096 bytes; fail loudly if exceeded
8. Append to segment file via `writer.Append()`
9. Execute corresponding SQL mutation via `executor.Execute()`
10. Print event JSON line to stdout
11. Exit 0 on success, non-zero on any error

`--project-root` tells Go where `.endless/events/` lives. Python passes `Path.cwd()` or the project's registered path.

**Go owns all write-supporting SQL.** Python passes intent, not data:
- Sort order: Python passes `--after 803` (optional), Go queries `MAX(sort_order)` and calculates
- Project ID: Python passes `--project endless`, Go resolves to project_id via `SELECT id FROM projects WHERE name = ?`
- Cascade: Python passes `--cascade`, Go does the recursive CTE
- Circular ref check: Go validates parent chain before move
- Child count: Go checks before delete

Python has zero SQLite imports for task write operations. Pure display reads (task list, task show) stay in Python for now (separate future task).

### internal/events/writer.go

```go
type Writer struct {
    eventsDir string   // .endless/events/
    nodeHex   string   // "a7f3"
    seq       int      // current segment sequence number
    count     int      // events in current segment
    maxCount  int      // rotation threshold (10000)
}

func NewWriter(projectRoot string, nodeHex string) (*Writer, error)
func (w *Writer) Append(line []byte) error   // O_APPEND write + rotation check
func (w *Writer) CurrentSegment() string     // current filename
```

**Segment naming:** `events-{nodeHex}-{seq:06d}.jsonl` (6-digit zero-padded sequence).

**NewWriter:** Scans existing segment files for this node to determine current seq and count. If no segments exist, starts at seq=1.

**Append:**
1. Check `len(line) > 4096`: return error with the line length and kind
2. Open file with `os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)`
3. Write `line` + `\n`
4. Close file
5. Increment count; if count >= maxCount, increment seq and reset count

**Rotation:** Simply incrementing seq means the next Append opens a new file. Old segments are never modified.

### internal/events/executor.go

Maps event kinds to SQL mutations against `monitor.DB()`.

```go
func Execute(evt *Event) error
```

Dispatches on `evt.Kind`:

| Kind | SQL | Notes |
|---|---|---|
| Kind | SQL | Go-side reads |
|---|---|---|
| `task.created` | `INSERT INTO tasks (...)` | Resolve project_id from name. Calculate sort_order: if `after_id` in payload, query that task's sort_order + 5; else `MAX(sort_order) + 10` for project+phase |
| `task.imported` | Same INSERT, different fields | Same project_id resolution |
| `task.status_changed` | `UPDATE tasks SET status=?, completed_at=?, tier=? WHERE id=?` or recursive CTE for cascade | Check task exists. For cascade: recursive CTE to find descendants |
| `task.fields_updated` | Dynamic `UPDATE tasks SET {fields} WHERE id=?` | Check task exists. Status can be in fields map (for update_plan mixed changes) |
| `task.moved` | `UPDATE tasks SET parent_id=? WHERE id=?` | Check task exists. Circular reference check: walk parent chain from target |
| `task.deleted` | `DELETE FROM tasks WHERE id=?` or recursive CTE for cascade | Check task exists. Count children for cascade decision |
| `task.bulk_cleared` | `DELETE FROM tasks WHERE project_id=? AND source_file=?` | Resolve project_id. NULL-out parent_ids first |

**All in one DB transaction.** Each executor call wraps reads + writes in `db.Begin()` / `tx.Commit()`. If any step fails, `tx.Rollback()` and return error. No partial state.

**Returning the new task ID:** For `task.created`, the executor returns `lastInsertId` which the binary prints as part of the output so Python can capture it.

### Output format

stdout on success:
```json
{"id":"E-857","ts":"0C8G1T3K0A1B2C3","kind":"task.created"}
```

Python parses this to get the new task ID (for created tasks) and the kairos timestamp (for logging).

stderr + exit code 1 on failure:
```
endless-event: error: {description}
```

## Python Implementation

### src/endless/event_bridge.py

```python
def emit_event(kind, project, entity_type, entity_id, actor_kind, actor_id,
               payload, project_root=None, cascade=False):
    """Shell out to endless-event to write event + execute DB mutation."""
    
    node_id = _get_or_create_node_id()
    if project_root is None:
        project_root = str(Path.cwd())
    
    cmd = [
        "endless-event", "emit",
        "--kind", kind,
        "--project", project,
        "--entity-type", entity_type,
        "--entity-id", str(entity_id),
        "--actor-kind", actor_kind,
        "--actor-id", actor_id,
        "--node-id", node_id,
        "--project-root", project_root,
        "--payload", json.dumps(payload),
    ]
    
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise click.ClickException(f"Event write failed: {result.stderr.strip()}")
    
    return json.loads(result.stdout) if result.stdout.strip() else None


def _get_or_create_node_id():
    """Read node_id from config.json, or generate and persist one."""
    config_path = Path.home() / ".config" / "endless" / "config.json"
    config = json.loads(config_path.read_text())
    if "node_id" not in config:
        import secrets
        config["node_id"] = secrets.token_hex(2)  # 4 hex chars
        config_path.write_text(json.dumps(config, indent=2))
    return config["node_id"]
```

### Changes to task_cmd.py

Each mutation function replaces its `db.execute()` call with `emit_event()`. Example for `add_item()`:

**Before:**
```python
cursor = db.execute(
    "INSERT INTO tasks (...) VALUES (...)",
    (project_id, phase, title, ...),
)
item_id = cursor.lastrowid
```

**After:**
```python
result = emit_event(
    kind="task.created",
    project=proj_name,
    entity_type="task",
    entity_id="0",  # new task, ID assigned by Go
    actor_kind="cli",
    actor_id=f"{os.getenv('USER', 'unknown')}@{socket.gethostname()}",
    payload={
        "title": title,
        "description": description or "",
        "phase": phase,
        "status": status,
        "type": task_type,
        "tier": tier,
        "parent_id": parent_id,
        "after_id": after,  # Go resolves to sort_order
    },
)
item_id = int(result["id"].replace("E-", ""))
```

Functions to change:
- `add_item()` -- task.created
- `_do_import()` -- task.imported (one per task in the tree)
- `complete_item()` -- task.status_changed (with cascade flag)
- `assume_item()` -- task.status_changed (with cascade flag)
- `start_item()` -- task.status_changed
- `update_plan()` -- task.status_changed and/or task.fields_updated
- `remove_item()` -- task.deleted (with cascade flag)
- `move_task()` -- task.moved

**Note on update_plan():** Always emits `task.fields_updated`, even when status is among the changed fields. `task.status_changed` is reserved for the dedicated status commands (complete, assume, start) which have richer semantics (cascade, completed_at, tier reset). One Python call = one Go subprocess call = one event = one DB transaction. No partial state.

## Build Changes

### justfile additions

```
# Build targets: add endless-event
build:
    ...existing...
    go build -o bin/endless-event ./cmd/endless-event

install:
    ...existing...
    ln -sfn "$(pwd)/bin/endless-event" /usr/local/bin/endless-event

# Go tests
test-go:
    go test ./internal/kairos/... ./internal/events/... -v

# All tests
test-all:
    just test
    just test-go
```

## Verification

```bash
# Go tests
go test ./internal/events/... -v -race

# Build
just build

# Manual test: emit a task.created event
endless-event emit \
  --kind task.created \
  --project endless \
  --entity-type task \
  --entity-id 0 \
  --actor-kind cli \
  --actor-id "mike@macbook" \
  --node-id a7f3 \
  --project-root /Users/mikeschinkel/Projects/endless \
  --payload '{"title":"Test task","phase":"now","status":"needs_plan","type":"task","sort_order":999}'

# Verify segment file created
ls .endless/events/

# Verify task in DB
endless task show <id>

# Python tests
just test

# End-to-end: add a task via Python CLI, verify both event file and DB
endless task add "Test dual-write task" --tier 1
cat .endless/events/events-*.jsonl | tail -1
```
