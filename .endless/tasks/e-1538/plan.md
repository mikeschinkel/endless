# E-1538 — TaskType enum + task_types FK mirror

This plan implements the project-wide posture in **ED-1506** (Consts in code are source-of-truth, NOT values stored in SQLite DB) as it applies to `tasks.type`. Sibling work for phases/statuses lives in E-1532 under the same decision.

Scope is intentionally narrow: schema + enum + FK + validator + startup check. Cleanup of currently-miscategorized records lives in sibling task E-1548 so this task stays small and unblocks E-1539 quickly.

## 1. The posture

Source of truth is a Go typed const `TaskType`. The `task_types` table exists solely to mirror the enum into SQL for FK enforcement and queryability. The table is downstream of the enum, never the other way around. Adding a type = change the enum + add a migration row.

## 2. Schema

```sql
CREATE TABLE task_types (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);

-- Seeded by schema.sql with idempotent inserts; values must match the Go enum.
INSERT OR IGNORE INTO task_types (id, slug, label) VALUES
    (1, 'task',     'Task'),
    (2, 'bug',      'Bug'),
    (3, 'research', 'Research'),
    (4, 'epic',     'Epic');

-- Migration: add type_id, backfill from current type values where authorized,
-- then drop the legacy type column.
ALTER TABLE tasks ADD COLUMN type_id INTEGER REFERENCES task_types(id);
```

Field naming:

- `slug` — the machine-stable lowercase identifier. Code, CLI args, JSON, and config use this. Must never change once shipped because external things depend on it.
- `label` — the human display string. UI / `task show` / list output render this.

FK shape: `INTEGER REFERENCES task_types(id)`, per existing endless schema conventions (memory `feedback_endless_schema_conventions` — `_id`-suffixed integer FKs).

## 3. Go-side enum

```go
type TaskType int

const (
    TaskTypeTask     TaskType = 1
    TaskTypeBug      TaskType = 2
    TaskTypeResearch TaskType = 3
    TaskTypeEpic     TaskType = 4
)

// String returns the lowercase machine slug (matches task_types.slug).
func (t TaskType) String() string {
    switch t {
    case TaskTypeTask:     return "task"
    case TaskTypeBug:      return "bug"
    case TaskTypeResearch: return "research"
    case TaskTypeEpic:     return "epic"
    default:               return fmt.Sprintf("TaskType(%d)", int(t))
    }
}

// ParseTaskType converts a slug from CLI / external input to a TaskType.
// Returns an error for unknown slugs; the validator (§4) calls this.
func ParseTaskType(s string) (TaskType, error) {
    switch s {
    case "task":     return TaskTypeTask, nil
    case "bug":      return TaskTypeBug, nil
    case "research": return TaskTypeResearch, nil
    case "epic":     return TaskTypeEpic, nil
    default:         return 0, fmt.Errorf("unknown task type: %q", s)
    }
}

// AllTaskTypes returns the canonical set. Used by the startup integrity check.
func AllTaskTypes() []TaskType {
    return []TaskType{TaskTypeTask, TaskTypeBug, TaskTypeResearch, TaskTypeEpic}
}
```

Underlying type is `int` with explicit numeric constants (no `iota` — see memory `feedback-go-enum-pattern`). All type-driven logic operates on `TaskType` values, never on raw strings from the DB. DB reads return integers cast to `TaskType`; if the integer doesn't match any known constant, the read fails (defensive — should never fire if startup check passed).

## 4. Validator

App-level validator on the write path calls `ParseTaskType` on incoming `--type <slug>` CLI arguments (or equivalent) and rejects unknown slugs with the actionable error before the DB is touched. The FK provides a second line of defense at the DB layer.

## 5. Startup integrity check

At process boot, compare `AllTaskTypes()` against the rows in `task_types`:

- For each `TaskType` constant: confirm a row exists in `task_types` with matching `id`. Also confirm `slug` matches `String()`.
- For each `task_types` row: confirm the `id` matches a known `TaskType` constant.

Hard-fail (process exit) on any mismatch. This catches "someone added a row to `task_types` without updating the enum" and vice versa. The slug-match check catches "enum and table drifted on the display string" (which would also fail the matching label check downstream).

## 6. Migration handling for miscategorized records

When the FK is enabled, existing rows with `type` values not in the seed slug set (`plan`, `chore`, miscategorized `bug` entries) would fail FK constraint on backfill. Approach: backfill `type_id` from current `type` values WHERE the current value maps to a known slug; for rows with unauthorized types, set `type_id` NULL temporarily and leave them for E-1548.

A migration-time integrity check (separate from the runtime startup check) logs the count of rows left with NULL `type_id` and which legacy `type` values they had, so E-1548 has a head start.

## 7. Decomposition

Single task, no children. The schema + enum + FK + validator + startup check are tightly coupled and ship together.

## 8. Unblocks / related

- **Unblocks**: E-1539 (Add epic value to task_types) — `epic` is in the seed set, so E-1539 reduces to verification. Other E-1537 children depend on E-1539.
- **Sibling cleanup**: E-1548 (cleans_up E-1538) — reclassify the 73 currently-miscategorized records left at NULL `type_id` by §6. Phase=later; does NOT block E-1537.
- **Sibling formalization**: E-1532 (phases/statuses) — same posture, parallel implementation, not blocked by this.

## 9. Out of scope

- Phases/statuses formalization (E-1532)
- `task_content` typed content (E-1531)
- Renaming the `type` column itself (replaced by `type_id`; legacy `type` dropped at migration end)
- Reclassifying the 73 miscategorized records (E-1548)
