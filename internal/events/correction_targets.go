package events

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/taskcontent"
)

// What `endless correct` may touch (E-2486).
//
// A target is one TABLE the ledger writes, not an entity type: one entity type
// writes several tables (task events write tasks, task_content, task_plan_items,
// task_landings and the reconciliation tables), and the first design, which
// targeted entity types, left task_plan_items uncorrectable — the table at the
// centre of the deadlock that prompted the command. Three entity types have no
// table at all (conversation, message, project_next) and take annotations only.
//
// Columns come from a list per target, never from the table: a wrong value in
// an unlisted column waits for a release. `--skip-gates` never skips the list.

// CorrectionTarget is one thing `endless correct` can address.
type CorrectionTarget struct {
	// Name is what the user types.
	Name string

	// Table is the table the target writes, or "" for an annotation-only entity
	// type.
	Table string

	// Key names the key columns, in the order the user joins them with `:`.
	// Empty for an annotation-only target.
	Key []string

	// Columns are the correctable columns, by their user-facing names. For the
	// task target that includes the content kinds, which live in task_content.
	Columns []string

	// Entity is the entity type a correction of this target is recorded
	// against.
	Entity EntityType

	// NoDelete, when set, is why a correcting delete of this target is
	// refused, in the words the refusal shows. Every other operation on the
	// target is unaffected. It carries the reason rather than a flag so the
	// refusal names a rule the user can read about, not a feature the
	// command lacks.
	NoDelete string
}

// taskColumns are the task target's columns: the tasks columns
// task.fields_updated accepts, the two that only a correction may write
// (completed_at, verified_sha), and every task content kind.
var taskColumns = func() []string {
	cols := []string{"title", "description", "phase", "type", "status", "parent_id",
		"complexity", "risk", "completed_at", "verified_sha"}
	return append(cols, taskcontent.Slugs()...)
}()

// correctionTargets is the closed list, in the order the plan gives it.
var correctionTargets = []CorrectionTarget{
	{Name: "task", Table: "tasks", Key: []string{"id"}, Columns: taskColumns, Entity: EntityTask},
	{Name: "task_dep", Table: "task_deps", Key: []string{"id"}, Columns: []string{"dep_type"}, Entity: EntityTaskDep},
	{Name: "project", Table: "projects", Key: []string{"id"},
		Columns: []string{"label", "group_name", "description", "status", "language"}, Entity: EntityProject},
	// Sessions are never deleted (ED-1655): fifteen INTEGER columns outside
	// sessions hold a session id and only four declare a foreign key, while
	// sessions.id is a rowid alias allocated as max(rowid) + 1, so deleting
	// the highest session hands its id to the next one created and the
	// undeclared references then name the wrong session while reading as
	// correct. Hiding is the reversible way to take one out of a listing.
	{Name: "session", Table: "sessions", Key: []string{"id"},
		Columns: []string{"state", "task_id", "focus_task_id", "hidden"}, Entity: EntitySession,
		// The text names the rule and not the decision's id: the user may be
		// running Endless on a project whose ledger is not this one.
		NoDelete: "sessions are never deleted: a freed session id is handed to the next session " +
			"created, and the rows that still hold the old id then name the wrong session; " +
			"a session that should not be listed is hidden instead"},
	{Name: "session_status", Table: "session_statuses", Key: []string{"id"},
		Columns: []string{"headline", "summary"}, Entity: EntitySessionStatus},
	{Name: "session_tasks", Table: "session_tasks", Key: []string{"id"},
		Columns: []string{"relation_id"}, Entity: EntitySessionTasks},
	{Name: "note", Table: "notes", Key: []string{"id"},
		Columns: []string{"message", "resolved", "resolved_at"}, Entity: EntityNote},
	{Name: "decision", Table: "decisions", Key: []string{"id"},
		Columns: []string{"title", "description", "text", "notes", "origin_task_id", "rejection_reason", "status"},
		Entity:  EntityDecision},
	{Name: "decision_relation", Table: "decision_relations", Key: []string{"id"},
		Columns: []string{"relation_type"}, Entity: EntityDecisionRelation},
	{Name: "task_question", Table: "task_questions", Key: []string{"id"},
		Columns: []string{"answer", "status", "reason"}, Entity: EntityTaskQuestion},
	{Name: "shipped_file", Table: "shipped_file_writes", Key: []string{"project_id", "path"},
		Columns: []string{"sha256"}, Entity: EntityShippedFile},
	// Keyed by the pair every surface shows (E-2486 P55 is task 2486, seq 55),
	// not by the row id nothing prints.
	{Name: "plan_item", Table: "task_plan_items", Key: []string{"task_id", "seq"},
		Columns: []string{"statement", "kind", "state", "verify", "detail", "approved_statement",
			"approved_kind", "approved_at", "post_land", "approved_post_land"}, Entity: EntityTask},
	{Name: "session_hidden_task", Table: "session_hidden_tasks", Key: []string{"session_id", "task_id"},
		Entity: EntitySessionTasks},
	{Name: "session_pinned_task", Table: "session_pinned_tasks", Key: []string{"session_id", "task_id"},
		Entity: EntitySessionTasks},
	{Name: "session_notice", Table: "session_notices", Key: []string{"id"},
		Columns: []string{"notified"}, Entity: EntitySession},
	{Name: "task_landing", Table: "task_landings", Key: []string{"id"},
		Columns: []string{"merge_commit_sha", "base_branch"}, Entity: EntityTask},
	{Name: "task_reconciliation", Table: "task_reconciliations", Key: []string{"id"},
		Columns: []string{"plan_digest", "head"}, Entity: EntityTask},
	{Name: "task_reconciliation_entry", Table: "task_reconciliation_entries",
		Key:     []string{"reconciliation_id", "item_seq"},
		Columns: []string{"verdict", "note", "holds_reason"}, Entity: EntityTask},
	{Name: "task_post_land_entry", Table: "task_post_land_entries", Key: []string{"id"},
		Columns: []string{"verdict", "note", "holds_reason"}, Entity: EntityTask},
	// Annotation-only: entity types the ledger names that have no table.
	{Name: "conversation", Entity: EntityConversation},
	{Name: "message", Entity: EntityMessage},
	{Name: "project_next", Entity: EntityProjectNext},
}

// CorrectionTargetByName resolves a target the user typed.
func CorrectionTargetByName(name string) (CorrectionTarget, bool) {
	for _, t := range correctionTargets {
		if t.Name == name {
			return t, true
		}
	}
	return CorrectionTarget{}, false
}

// CorrectionTargetNames lists every target name, table targets first in the
// order of the list, then the annotation-only ones.
func CorrectionTargetNames() []string {
	names := make([]string, 0, len(correctionTargets))
	for _, t := range correctionTargets {
		names = append(names, t.Name)
	}
	return names
}

// CorrectionTargets returns the list, for help text and tests.
func CorrectionTargets() []CorrectionTarget {
	out := make([]CorrectionTarget, len(correctionTargets))
	copy(out, correctionTargets)
	return out
}

// Annotates reports whether the target takes annotations only.
func (t CorrectionTarget) Annotates() bool { return t.Table == "" }

// hasColumn reports whether name is on the target's list.
func (t CorrectionTarget) hasColumn(name string) bool {
	for _, c := range t.Columns {
		if c == name {
			return true
		}
	}
	return false
}

// unknownTarget is the refusal for a target name not on the list.
func unknownTarget(name string) error {
	return refusal.NoReport(
		fmt.Sprintf("correct: unknown target %q; the targets are %s", name,
			strings.Join(CorrectionTargetNames(), ", ")),
		"Retry with one of the listed targets")
}

// unlistedColumn is the refusal for a column the target does not allow, with
// or without --skip-gates. It names the columns that are allowed.
func unlistedColumn(t CorrectionTarget, col string, allowed []string) error {
	if len(allowed) == 0 {
		return refusal.NoReport(
			fmt.Sprintf("correct: %s has no correctable columns; a %s row is inserted or deleted whole", t.Name, t.Name),
			"Retry with --insert or --delete, or correct a target that lists the column")
	}
	return refusal.NoReport(
		fmt.Sprintf("correct: %q is not a column %s allows; allowed: %s", col, t.Name,
			strings.Join(allowed, ", ")),
		"Retry naming one of the allowed columns")
}

// ---------------------------------------------------------------------------
// Table shape, read from SQLite rather than repeated here

// columnInfo is one row of PRAGMA table_info.
type columnInfo struct {
	Name       string
	Type       string
	NotNull    bool
	HasDefault bool
	PK         int
}

// tableShape is a table's columns in declaration order, by name.
type tableShape struct {
	Table   string
	Order   []string
	Columns map[string]columnInfo
}

// SoftRemoves reports whether the table keeps removed rows behind a removed
// column instead of deleting them (tasks per ED-1547, decisions per E-2010).
// A correcting delete on such a table sets removed = 1, the way the table's
// own remove command does.
func (s *tableShape) SoftRemoves() bool {
	_, ok := s.Columns["removed"]
	return ok
}

// TableSoftRemoves reports whether a correcting delete on table marks the row
// removed = 1 rather than deleting it. Exported for tests.
func TableSoftRemoves(db dbQuerier, table string) (bool, error) {
	shape, err := readTableShape(db, table)
	if err != nil {
		return false, err
	}
	return shape.SoftRemoves(), nil
}

// readTableShape reads PRAGMA table_info for one table.
func readTableShape(db dbQuerier, table string) (*tableShape, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, fmt.Errorf("correct: read columns of %s: %w", table, err)
	}
	defer rows.Close()
	shape := &tableShape{Table: table, Columns: map[string]columnInfo{}}
	for rows.Next() {
		var cid int
		var ci columnInfo
		var notNull, pk int
		var typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &ci.Name, &typ, &notNull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("correct: scan columns of %s: %w", table, err)
		}
		ci.Type = strings.ToUpper(typ)
		ci.NotNull = notNull == 1
		ci.HasDefault = dflt.Valid
		ci.PK = pk
		shape.Order = append(shape.Order, ci.Name)
		shape.Columns[ci.Name] = ci
	}
	if len(shape.Order) == 0 {
		return nil, fmt.Errorf("correct: table %s has no columns (does it exist?)", table)
	}
	return shape, rows.Err()
}

// isInteger reports whether the column's declared type has INTEGER affinity.
func (c columnInfo) isInteger() bool { return strings.Contains(c.Type, "INT") }

// allocatedID reports whether the column is the table's single INTEGER
// PRIMARY KEY, which an insert never names: it is allocated under the lock.
func (s *tableShape) allocatedID() (string, bool) {
	var pks []string
	for _, name := range s.Order {
		if s.Columns[name].PK > 0 {
			pks = append(pks, name)
		}
	}
	if len(pks) == 1 && s.Columns[pks[0]].isInteger() {
		return pks[0], true
	}
	return "", false
}

// required lists the columns an insert must supply: NOT NULL without a
// default, minus the allocated id. Key columns of a composite key are among
// them in every table on the list.
func (s *tableShape) required() []string {
	allocated, _ := s.allocatedID()
	var out []string
	for _, name := range s.Order {
		c := s.Columns[name]
		if name == allocated {
			continue
		}
		if c.NotNull && !c.HasDefault {
			out = append(out, name)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Keys

// idPrefixRE strips a display prefix from a numeric key part: E-2486, ED-12,
// EQ-7, ES-3, P55, R2 all name the number after them.
var idPrefixRE = regexp.MustCompile(`^[A-Za-z]{1,2}-?(\d+)$`)

// parseKey splits the key the user typed into the target's key columns, typed
// per the table's declared types. A composite key takes its parts joined by
// `:`; only the last part may itself contain one.
func parseKey(t CorrectionTarget, shape *tableShape, raw string) (map[string]any, error) {
	parts := strings.SplitN(raw, ":", len(t.Key))
	if len(parts) != len(t.Key) || raw == "" {
		return nil, refusal.NoReport(
			fmt.Sprintf("correct: a %s row is keyed by %s, got %q", t.Name, strings.Join(t.Key, ":"), raw),
			fmt.Sprintf("Retry with the key as %s", strings.Join(t.Key, ":")))
	}
	key := make(map[string]any, len(parts))
	for i, col := range t.Key {
		v, err := coerceValue(parts[i], shape.Columns[col])
		if err != nil {
			return nil, refusal.NoReport(
				fmt.Sprintf("correct: key part %s of %s: %s", col, t.Name, err), "Retry with a numeric id")
		}
		if v == nil {
			return nil, refusal.NoReport(
				fmt.Sprintf("correct: key part %s of %s is empty", col, t.Name), "Retry with the key supplied")
		}
		key[col] = v
	}
	return key, nil
}

// keyString renders a key the way the payload and the output carry it.
func keyString(t CorrectionTarget, key map[string]any) string {
	parts := make([]string, len(t.Key))
	for i, col := range t.Key {
		parts[i] = fmt.Sprint(key[col])
	}
	return strings.Join(parts, ":")
}

// coerceValue turns a value the user typed into the value the column stores:
// NULL (exactly) is SQL NULL; an INTEGER column takes a number, true/false, or
// a display id; anything else is kept as text.
func coerceValue(raw string, col columnInfo) (any, error) {
	if raw == "NULL" {
		return nil, nil
	}
	if !col.isInteger() {
		return raw, nil
	}
	switch strings.ToLower(raw) {
	case "true":
		return int64(1), nil
	case "false":
		return int64(0), nil
	}
	if m := idPrefixRE.FindStringSubmatch(raw); m != nil {
		raw = m[1]
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%s is an integer column, got %q", col.Name, raw)
	}
	return n, nil
}

// normalizeValue brings a value read back from a JSON payload or the database
// to the form comparisons and bindings use: integral floats and []byte become
// int64 and string.
func normalizeValue(v any) any {
	switch x := v.(type) {
	case float64:
		if x == float64(int64(x)) {
			return int64(x)
		}
		return x
	case []byte:
		return string(x)
	case int:
		return int64(x)
	case bool:
		if x {
			return int64(1)
		}
		return int64(0)
	}
	return v
}

// sortedColumns returns a map's keys in order, for stable SQL and messages.
func sortedColumns(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
