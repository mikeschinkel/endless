package events

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mikeschinkel/endless/internal/planitem"
	"github.com/mikeschinkel/endless/internal/questionstatus"
	"github.com/mikeschinkel/endless/internal/rating"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/sessionstate"
	"github.com/mikeschinkel/endless/internal/taskcontent"
	"github.com/mikeschinkel/endless/internal/taskstatus"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

// Correcting ledger state (E-2486).
//
// The ledger is append-only, and until this the only way to correct a row was
// to replay ordinary transitions through their gates — which can deadlock when
// a gate was skipped on the way in, and leaves transitions in the history that
// describe no real event. A correction instead posts two kinds of entry, so
// that replay and audit each read only what they need:
//
//   - ledger.row_changed (RowChangedPayload): the change, applied by the
//     executor and the projector through ONE function, applyRowChanged.
//   - ledger.corrected (CorrectedPayload): the reason, the before values, and
//     the byte offset of each change entry; a no-op for both.
//
// PrepareCorrection does everything that can refuse — under the write lock,
// before anything reaches the ledger — so a refused correction leaves nothing
// behind. Gates (the task transition table, plan-item approval, the status and
// phase vocabularies) refuse by default and `--skip-gates` bypasses them, which
// the correction entry records. Data invariants (the column list, an existing
// row, no duplicate key, no dangling reference, no parent cycle) hold either
// way.

// Row operations.
const (
	RowOpUpdate = "update"
	RowOpInsert = "insert"
	RowOpDelete = "delete"
)

// RowChangedPayload is one row of one target, written as the user said. For an
// update, Values holds the columns set; for an insert, every column written,
// the allocated id included; for a delete, nothing.
type RowChangedPayload struct {
	Target string         `json:"target"`
	Key    string         `json:"key"`
	Op     string         `json:"op"`
	Values map[string]any `json:"values,omitempty"`
}

// CorrectedPayload is the correction: why, what the rows held before, whether
// gates were skipped, and where its change entries start in the segment both
// were written to. Before[i] belongs to Refs[i]. An annotation has neither.
type CorrectedPayload struct {
	Reason    string      `json:"reason"`
	SkipGates bool        `json:"skip_gates,omitempty"`
	Before    []RowBefore `json:"before,omitempty"`
	Refs      []int64     `json:"refs"`
}

// RowBefore is what one changed row held before: the columns an update set,
// the whole row a delete removed, nothing for an insert.
type RowBefore struct {
	Target string         `json:"target"`
	Key    string         `json:"key"`
	Values map[string]any `json:"values,omitempty"`
}

// CorrectionRequest is what the user asked for, with values still as typed.
type CorrectionRequest struct {
	Target    string
	Key       string
	Op        string // RowOpUpdate, RowOpInsert, RowOpDelete, or "" for an annotation
	Values    map[string]string
	Reason    string
	SkipGates bool

	// Project is the project the command runs against. A row that belongs to
	// another project is refused, so an entity's correction lands in its own
	// project's ledger.
	Project string
}

// PreparedCorrection is a correction checked against the current rows and
// ready to record.
type PreparedCorrection struct {
	Target CorrectionTarget
	Entity EntityRef

	// Change is nil for an annotation.
	Change *RowChangedPayload
	Before *RowBefore

	// Summary is one line naming what changes, for the command's output.
	Summary string

	// Notes are things the user should hear after the correction, such as
	// that an epic's status may be re-derived.
	Notes []string

	// TaskIDs and DecisionIDs name the records whose document mirrors the
	// correction may have changed.
	TaskIDs     []int64
	DecisionIDs []int64
}

// PrepareCorrection validates a request against the current rows and builds
// the entries to record. Call it under BEGIN IMMEDIATE on db: what it reads
// (the before values, an allocated id) must still hold when the change applies.
func PrepareCorrection(db dbQuerier, req CorrectionRequest) (*PreparedCorrection, error) {
	target, ok := CorrectionTargetByName(req.Target)
	if !ok {
		return nil, unknownTarget(req.Target)
	}
	if strings.TrimSpace(req.Reason) == "" {
		return nil, refusal.NoReport("correct: --reason is required; a correction records why",
			"Retry with --reason <text>")
	}
	if target.Annotates() {
		return prepareAnnotation(target, req)
	}
	shape, err := readTableShape(db, target.Table)
	if err != nil {
		return nil, err
	}
	switch req.Op {
	case "":
		key, row, err := loadRow(db, target, shape, req.Key)
		if err != nil {
			return nil, err
		}
		if err := requireSameProject(db, target, req.Project, row); err != nil {
			return nil, err
		}
		entity, err := entityFor(db, target, key, row)
		if err != nil {
			return nil, err
		}
		return &PreparedCorrection{
			Target:  target,
			Entity:  entity,
			Summary: fmt.Sprintf("Annotated %s %s", target.Name, keyString(target, key)),
		}, nil
	case RowOpUpdate:
		return prepareUpdate(db, target, shape, req)
	case RowOpInsert:
		return prepareInsert(db, target, shape, req)
	case RowOpDelete:
		return prepareDelete(db, target, shape, req)
	}
	return nil, refusal.Faultf("correct: unknown operation %q", req.Op)
}

// prepareAnnotation records a correction entry against an entity type that
// has no table: any id, since there is no row to check it against.
func prepareAnnotation(target CorrectionTarget, req CorrectionRequest) (*PreparedCorrection, error) {
	if req.Op != "" {
		return nil, refusal.NoReport(
			fmt.Sprintf("correct: %s has no table; it takes annotations only", target.Name),
			"Retry without --set, --insert or --delete")
	}
	if strings.TrimSpace(req.Key) == "" {
		return nil, refusal.NoReport(
			fmt.Sprintf("correct: an annotation of a %s names the %s id", target.Name, target.Name),
			"Retry with the id after the target")
	}
	return &PreparedCorrection{
		Target:  target,
		Entity:  EntityRef{Type: target.Entity, ID: req.Key},
		Summary: fmt.Sprintf("Annotated %s %s", target.Name, req.Key),
	}, nil
}

// loadRow parses the key and reads the row it names, whole.
func loadRow(db dbQuerier, t CorrectionTarget, shape *tableShape, rawKey string) (map[string]any, map[string]any, error) {
	if strings.TrimSpace(rawKey) == "" {
		return nil, nil, refusal.NoReport(
			fmt.Sprintf("correct: name the %s row to correct, keyed by %s", t.Name, strings.Join(t.Key, ":")),
			"Retry with the key after the target")
	}
	key, err := parseKey(t, shape, rawKey)
	if err != nil {
		return nil, nil, err
	}
	row, err := readRow(db, shape, t, key)
	if err != nil {
		return nil, nil, err
	}
	if row == nil {
		return nil, nil, refusal.NoReport(
			fmt.Sprintf("correct: no %s row with %s %s", t.Name, strings.Join(t.Key, ":"), keyString(t, key)),
			"Retry with the key of a row that exists")
	}
	return key, row, nil
}

// readRow reads one row by key, or nil when there is none.
func readRow(db dbQuerier, shape *tableShape, t CorrectionTarget, key map[string]any) (map[string]any, error) {
	where, args := keyWhere(t, key)
	rows, err := db.Query(fmt.Sprintf("SELECT %s FROM %s WHERE %s",
		strings.Join(shape.Order, ", "), shape.Table, where), args...)
	if err != nil {
		return nil, fmt.Errorf("correct: read %s: %w", t.Name, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	vals := make([]any, len(shape.Order))
	ptrs := make([]any, len(shape.Order))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, fmt.Errorf("correct: scan %s: %w", t.Name, err)
	}
	row := make(map[string]any, len(shape.Order))
	for i, col := range shape.Order {
		row[col] = normalizeValue(vals[i])
	}
	return row, nil
}

// keyWhere renders the key as a WHERE clause.
func keyWhere(t CorrectionTarget, key map[string]any) (string, []any) {
	parts := make([]string, len(t.Key))
	args := make([]any, len(t.Key))
	for i, col := range t.Key {
		parts[i] = col + " = ?"
		args[i] = key[col]
	}
	return strings.Join(parts, " AND "), args
}

// ---------------------------------------------------------------------------
// Update

func prepareUpdate(db dbQuerier, t CorrectionTarget, shape *tableShape, req CorrectionRequest) (*PreparedCorrection, error) {
	key, row, err := loadRow(db, t, shape, req.Key)
	if err != nil {
		return nil, err
	}
	if err := requireSameProject(db, t, req.Project, row); err != nil {
		return nil, err
	}
	values, err := typedValues(t, shape, req.Values, t.Columns)
	if err != nil {
		return nil, err
	}
	before, err := beforeValues(db, t, row, values)
	if err != nil {
		return nil, err
	}
	// A --set that changes nothing is refused: a correction entry claiming a
	// change that did not happen would be the one kind of lie this command
	// exists to stop.
	var changed []string
	for _, col := range sortedColumns(values) {
		if !sameValue(before[col], values[col]) {
			changed = append(changed, col)
		}
	}
	if len(changed) == 0 {
		return nil, refusal.NoReport(
			fmt.Sprintf("correct: the --set values already hold on %s %s; nothing to correct",
				t.Name, keyString(t, key)),
			"Retry with a value that differs from the row, or record an annotation with no --set")
	}
	if err := checkInvariants(db, t, key, row, values); err != nil {
		return nil, err
	}
	var notes []string
	if !req.SkipGates {
		if err := checkGates(db, t, row, values); err != nil {
			return nil, err
		}
	}
	notes = append(notes, epicNote(t, row, values)...)
	entity, err := entityFor(db, t, key, row)
	if err != nil {
		return nil, err
	}
	ks := keyString(t, key)
	prep := &PreparedCorrection{
		Target:  t,
		Entity:  entity,
		Change:  &RowChangedPayload{Target: t.Name, Key: ks, Op: RowOpUpdate, Values: values},
		Before:  &RowBefore{Target: t.Name, Key: ks, Values: before},
		Summary: fmt.Sprintf("Corrected %s %s: %s", t.Name, ks, describeChanges(before, values, changed)),
		Notes:   notes,
	}
	prep.TaskIDs, prep.DecisionIDs = mirrorScope(t, row, values)
	return prep, nil
}

// typedValues parses --set/--insert pairs against the table's declared types,
// refusing any column off the allowed list.
func typedValues(t CorrectionTarget, shape *tableShape, raw map[string]string, allowed []string) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, refusal.NoReport(
			fmt.Sprintf("correct: no column=value pairs given for %s", t.Name),
			"Retry with --set col=value (or --insert col=value)")
	}
	values := make(map[string]any, len(raw))
	for col, v := range raw {
		if !contains(allowed, col) {
			return nil, unlistedColumn(t, col, allowed)
		}
		ci, isColumn := shape.Columns[col]
		if !isColumn {
			// A task content kind, or a field a table column is translated
			// from: kept as text, translated at apply.
			ci = columnInfo{Name: col, Type: "TEXT"}
		}
		typed, err := coerceValue(v, ci)
		if err != nil {
			return nil, refusal.NoReport("correct: "+err.Error(), "Retry with a value of the column's type")
		}
		values[col] = typed
	}
	return values, nil
}

// beforeValues reads what the row holds for each column an update sets, in
// the same vocabulary the values use (type and rating slugs, content text).
func beforeValues(db dbQuerier, t CorrectionTarget, row map[string]any, values map[string]any) (map[string]any, error) {
	before := make(map[string]any, len(values))
	for col := range values {
		if t.Name == "task" {
			v, handled, err := taskBeforeValue(db, row, col)
			if err != nil {
				return nil, err
			}
			if handled {
				before[col] = v
				continue
			}
		}
		before[col] = row[col]
	}
	return before, nil
}

// taskBeforeValue resolves the task target's translated columns: the type and
// rating slugs behind their id columns, and content text behind task_content.
func taskBeforeValue(db dbQuerier, row map[string]any, col string) (any, bool, error) {
	switch col {
	case "type":
		id, ok := row["type_id"].(int64)
		if !ok {
			return nil, true, nil
		}
		return tasktype.TaskType(id).String(), true, nil
	}
	if axis, ok := rating.AxisForField(col); ok {
		id, ok := row[axis.Column].(int64)
		if !ok {
			return nil, true, nil
		}
		return rating.Level(id).String(), true, nil
	}
	if name, ok := taskContentFields[col]; ok {
		var content sql.NullString
		err := db.QueryRow("SELECT content FROM task_content WHERE task_id = ? AND name = ?",
			row["id"], name.Slug()).Scan(&content)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, true, fmt.Errorf("correct: read task content %s: %w", col, err)
		}
		if !content.Valid {
			return "", true, nil
		}
		return content.String, true, nil
	}
	return nil, false, nil
}

// sameValue compares a before value with the value an update would write.
func sameValue(a, b any) bool {
	a, b = normalizeValue(a), normalizeValue(b)
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// describeChanges renders `col old → new` for each changed column.
func describeChanges(before, values map[string]any, changed []string) string {
	parts := make([]string, len(changed))
	for i, col := range changed {
		parts[i] = fmt.Sprintf("%s %s → %s", col, renderValue(before[col]), renderValue(values[col]))
	}
	return strings.Join(parts, ", ")
}

// renderValue shows a value in a summary line: NULL for nil, long text cut.
func renderValue(v any) string {
	if v == nil {
		return "NULL"
	}
	s := strings.Join(strings.Fields(fmt.Sprint(v)), " ")
	if len([]rune(s)) > 40 {
		s = truncateEnd(s, 40)
	}
	if s == "" {
		return `""`
	}
	return s
}

// ---------------------------------------------------------------------------
// Insert

func prepareInsert(db dbQuerier, t CorrectionTarget, shape *tableShape, req CorrectionRequest) (*PreparedCorrection, error) {
	if strings.TrimSpace(req.Key) != "" {
		return nil, refusal.NoReport(
			fmt.Sprintf("correct: --insert takes no key; a new %s row is keyed by its values", t.Name),
			"Retry without the key, giving key columns as --insert col=value")
	}
	allocated, hasAllocated := shape.allocatedID()
	allowed := append(append([]string{}, t.Columns...), shape.required()...)
	for _, col := range t.Key {
		if col != allocated && !contains(allowed, col) {
			allowed = append(allowed, col)
		}
	}
	values, err := typedValues(t, shape, req.Values, allowed)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, col := range shape.required() {
		if _, ok := values[col]; !ok {
			missing = append(missing, col)
		}
	}
	if len(missing) > 0 {
		return nil, refusal.NoReport(
			fmt.Sprintf("correct: an inserted %s row needs %s", t.Name, strings.Join(missing, ", ")),
			"Retry supplying every required column")
	}
	if hasAllocated {
		var next int64
		if err := db.QueryRow(fmt.Sprintf("SELECT COALESCE(MAX(%s), 0) + 1 FROM %s", allocated, shape.Table)).
			Scan(&next); err != nil {
			return nil, fmt.Errorf("correct: allocate %s id: %w", t.Name, err)
		}
		values[allocated] = next
	}
	key := make(map[string]any, len(t.Key))
	for _, col := range t.Key {
		key[col] = values[col]
	}
	if existing, err := readRow(db, shape, t, key); err != nil {
		return nil, err
	} else if existing != nil {
		return nil, refusal.NoReport(
			fmt.Sprintf("correct: a %s row with %s %s already exists", t.Name,
				strings.Join(t.Key, ":"), keyString(t, key)),
			"Retry with --set on that row, or different key values")
	}
	if err := checkInvariants(db, t, key, nil, values); err != nil {
		return nil, err
	}
	if err := requireSameProject(db, t, req.Project, values); err != nil {
		return nil, err
	}
	if !req.SkipGates {
		if err := checkGates(db, t, nil, values); err != nil {
			return nil, err
		}
	}
	entity, err := entityFor(db, t, key, values)
	if err != nil {
		return nil, err
	}
	ks := keyString(t, key)
	prep := &PreparedCorrection{
		Target:  t,
		Entity:  entity,
		Change:  &RowChangedPayload{Target: t.Name, Key: ks, Op: RowOpInsert, Values: values},
		Before:  &RowBefore{Target: t.Name, Key: ks},
		Summary: fmt.Sprintf("Inserted %s %s", t.Name, ks),
		Notes:   epicNote(t, nil, values),
	}
	prep.TaskIDs, prep.DecisionIDs = mirrorScope(t, values, values)
	return prep, nil
}

// ---------------------------------------------------------------------------
// Delete

func prepareDelete(db dbQuerier, t CorrectionTarget, shape *tableShape, req CorrectionRequest) (*PreparedCorrection, error) {
	if len(req.Values) > 0 {
		return nil, refusal.NoReport("correct: --delete takes no column=value pairs",
			"Retry with --delete alone, or --set without --delete")
	}
	key, row, err := loadRow(db, t, shape, req.Key)
	if err != nil {
		return nil, err
	}
	if err := requireSameProject(db, t, req.Project, row); err != nil {
		return nil, err
	}
	if shape.SoftRemoves() && row["removed"] == int64(1) {
		return nil, refusal.NoReport(
			fmt.Sprintf("correct: %s %s is already removed", t.Name, keyString(t, key)),
			fmt.Sprintf("Nothing to delete; retry on a live %s", t.Name))
	}
	refs, err := referrers(db, t, key)
	if err != nil {
		return nil, err
	}
	if len(refs) > 0 {
		return nil, refusal.NoReport(
			fmt.Sprintf("correct: other rows refer to %s %s: %s", t.Name, keyString(t, key),
				strings.Join(refs, "; ")),
			"Correct or delete the referring rows first, then retry")
	}
	before := make(map[string]any, len(row))
	for col, v := range row {
		before[col] = v
	}
	if t.Name == "task" {
		content, err := taskContentRows(db, row["id"])
		if err != nil {
			return nil, err
		}
		for name, text := range content {
			before[name] = text
		}
	}
	entity, err := entityFor(db, t, key, row)
	if err != nil {
		return nil, err
	}
	ks := keyString(t, key)
	summary := fmt.Sprintf("Deleted %s %s", t.Name, ks)
	if shape.SoftRemoves() {
		// A table with a removed column never loses a row (ED-1547 for tasks;
		// E-2010 extends it): the row stays with removed = 1 so its id is never
		// minted again. A correction deletes such a row the way its own remove
		// command does.
		summary = fmt.Sprintf("Removed %s %s (removed = 1; the id is never reused)", t.Name, ks)
	}
	prep := &PreparedCorrection{
		Target:  t,
		Entity:  entity,
		Change:  &RowChangedPayload{Target: t.Name, Key: ks, Op: RowOpDelete},
		Before:  &RowBefore{Target: t.Name, Key: ks, Values: before},
		Summary: summary,
	}
	prep.TaskIDs, prep.DecisionIDs = mirrorScope(t, row, nil)
	return prep, nil
}

// taskContentRows reads every content row of a task, by content slug.
func taskContentRows(db dbQuerier, taskID any) (map[string]string, error) {
	rows, err := db.Query("SELECT name, content FROM task_content WHERE task_id = ?", taskID)
	if err != nil {
		return nil, fmt.Errorf("correct: read task content: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, content string
		if err := rows.Scan(&name, &content); err != nil {
			return nil, err
		}
		out[name] = content
	}
	return out, rows.Err()
}

// undeclaredRef is a reference the schema holds without a FOREIGN KEY. The
// delete refusal and the dangling-id check both read this list alongside
// PRAGMA foreign_key_list.
type undeclaredRef struct {
	Table, Column, RefTable, RefColumn string
	Where                              string // an extra condition on Table, or ""
}

var undeclaredRefs = []undeclaredRef{
	{"session_tasks", "task_id", "tasks", "id", ""},
	{"session_tasks", "session_id", "sessions", "id", ""},
	{"session_notices", "task_id", "tasks", "id", ""},
	{"session_notices", "session_id", "sessions", "id", ""},
	{"session_hidden_tasks", "task_id", "tasks", "id", ""},
	{"session_hidden_tasks", "session_id", "sessions", "id", ""},
	{"session_pinned_tasks", "task_id", "tasks", "id", ""},
	{"session_pinned_tasks", "session_id", "sessions", "id", ""},
	{"session_statuses", "session_id", "sessions", "id", ""},
	{"task_deps", "source_id", "tasks", "id", "source_type = 'task'"},
	{"task_deps", "target_id", "tasks", "id", "target_type = 'task'"},
	{"decision_relations", "target_id", "tasks", "id", "target_kind = 'task'"},
	{"decision_relations", "target_id", "decisions", "id", "target_kind = 'decision'"},
	{"task_plan_items", "created_by_session", "sessions", "id", ""},
	{"task_questions", "asked_by_session", "sessions", "id", ""},
	{"task_reconciliations", "session_id", "sessions", "id", ""},
	{"task_post_land_entries", "session_id", "sessions", "id", ""},
}

// referrers lists the rows that refer to the keyed row: through every declared
// foreign key to its table, and through the undeclared references above. Each
// entry reads like `3 session_tasks rows (task_id)`.
func referrers(db dbQuerier, t CorrectionTarget, key map[string]any) ([]string, error) {
	var out []string
	count := func(table, column, where string, value any) error {
		q := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = ?", table, column)
		if where != "" {
			q += " AND " + where
		}
		var n int
		if err := db.QueryRow(q, value).Scan(&n); err != nil {
			return fmt.Errorf("correct: count %s.%s referrers: %w", table, column, err)
		}
		if n > 0 {
			out = append(out, fmt.Sprintf("%d %s row(s) by %s", n, table, column))
		}
		return nil
	}
	tables, err := allTables(db)
	if err != nil {
		return nil, err
	}
	for _, table := range tables {
		if t.Name == "task" && table == "task_content" {
			// The task target writes both tables: its content is the row's
			// own, not another row referring to it.
			continue
		}
		fks, err := foreignKeys(db, table)
		if err != nil {
			return nil, err
		}
		for _, fk := range fks {
			if fk.RefTable != t.Table {
				continue
			}
			refCol := fk.RefColumn
			if refCol == "" {
				refCol = t.Key[0]
			}
			value, ok := key[refCol]
			if !ok {
				continue
			}
			if err := count(table, fk.Column, "", value); err != nil {
				return nil, err
			}
		}
	}
	for _, r := range undeclaredRefs {
		if r.RefTable != t.Table {
			continue
		}
		value, ok := key[r.RefColumn]
		if !ok {
			continue
		}
		if err := count(r.Table, r.Column, r.Where, value); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// foreignKey is one row of PRAGMA foreign_key_list, single-column.
type foreignKey struct {
	Column, RefTable, RefColumn string
}

func foreignKeys(db dbQuerier, table string) ([]foreignKey, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA foreign_key_list(%s)", table))
	if err != nil {
		return nil, fmt.Errorf("correct: read foreign keys of %s: %w", table, err)
	}
	defer rows.Close()
	var out []foreignKey
	for rows.Next() {
		var id, seq int
		var refTable, from string
		var to sql.NullString
		var onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &refTable, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return nil, fmt.Errorf("correct: scan foreign keys of %s: %w", table, err)
		}
		out = append(out, foreignKey{Column: from, RefTable: refTable, RefColumn: to.String})
	}
	return out, rows.Err()
}

// allTables lists the database's tables.
func allTables(db dbQuerier) ([]string, error) {
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return nil, fmt.Errorf("correct: list tables: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Invariants: hold with or without --skip-gates

// checkInvariants refuses what no correction may write: a type or rating slug
// the enum tables do not hold, a reference to a row that does not exist through
// an undeclared reference (declared foreign keys refuse at the write), and a
// parent cycle. row is nil for an insert.
func checkInvariants(db dbQuerier, t CorrectionTarget, key, row, values map[string]any) error {
	if t.Name == "task" {
		if v, ok := values["type"]; ok && v != nil {
			if _, err := tasktype.Parse(fmt.Sprint(v)); err != nil {
				return refusal.NoReport("correct: "+err.Error(), "Retry with a task type slug")
			}
		}
		for _, axis := range rating.Axes() {
			if v, ok := values[axis.Field]; ok {
				if _, err := axis.ColumnValue(v); err != nil {
					return refusal.NoReport("correct: "+err.Error(), "Retry with low, medium, high or NULL")
				}
			}
		}
		if v, ok := values["parent_id"]; ok && v != nil && row != nil {
			pid := v.(int64)
			if err := ValidateNoParentCycle(db, key["id"].(int64), &pid); err != nil {
				return err
			}
		}
	}
	for _, r := range undeclaredRefs {
		if r.Table != t.Table {
			continue
		}
		v, ok := values[r.Column]
		if !ok || v == nil {
			continue
		}
		var n int
		if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = ?", r.RefTable, r.RefColumn), v).
			Scan(&n); err != nil {
			return fmt.Errorf("correct: check %s.%s: %w", r.Table, r.Column, err)
		}
		if n == 0 {
			return refusal.NoReport(
				fmt.Sprintf("correct: %s %v names no %s row", r.Column, v, r.RefTable),
				"Retry with the id of a row that exists")
		}
	}
	return nil
}

// requireSameProject refuses a row of another project: the entry would land
// in this project's ledger while describing that one's row.
func requireSameProject(db dbQuerier, t CorrectionTarget, project string, row map[string]any) error {
	if project == "" || row == nil {
		return nil
	}
	projectID, ok, err := rowProjectID(db, t, row)
	if err != nil || !ok {
		return err
	}
	var name string
	err = db.QueryRow("SELECT name FROM projects WHERE id = ?", projectID).Scan(&name)
	if err != nil {
		// A project the row names and the table does not hold: nothing to
		// compare against, and not this check's defect to report.
		return nil
	}
	if name != project {
		return refusal.NoReport(
			fmt.Sprintf("correct: that %s row belongs to project %s, not %s", t.Name, name, project),
			fmt.Sprintf("Retry with --project %s", name))
	}
	return nil
}

// rowProjectID finds the project a row belongs to, through its own project_id,
// its task, its session, or its reconciliation. ok is false when the target
// has no route to a project.
func rowProjectID(db dbQuerier, t CorrectionTarget, row map[string]any) (int64, bool, error) {
	if t.Table == "projects" {
		id, ok := row["id"].(int64)
		return id, ok, nil
	}
	if v, ok := row["project_id"].(int64); ok {
		return v, true, nil
	}
	lookup := func(q string, arg any) (int64, bool, error) {
		var id sql.NullInt64
		err := db.QueryRow(q, arg).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !id.Valid) {
			return 0, false, nil
		}
		if err != nil {
			return 0, false, fmt.Errorf("correct: resolve project: %w", err)
		}
		return id.Int64, true, nil
	}
	if v, ok := row["task_id"].(int64); ok {
		return lookup("SELECT project_id FROM tasks WHERE id = ?", v)
	}
	if v, ok := row["reconciliation_id"].(int64); ok {
		return lookup(`SELECT t.project_id FROM task_reconciliations r
		                 JOIN tasks t ON t.id = r.task_id WHERE r.id = ?`, v)
	}
	if t.Table == "task_deps" {
		if v, ok := row["source_id"].(int64); ok {
			return lookup("SELECT project_id FROM tasks WHERE id = ?", v)
		}
	}
	if v, ok := row["session_id"].(int64); ok {
		return lookup("SELECT project_id FROM sessions WHERE id = ?", v)
	}
	if t.Table == "sessions" {
		id, ok := row["project_id"].(int64)
		return id, ok, nil
	}
	return 0, false, nil
}

// ---------------------------------------------------------------------------
// Gates: refuse by default, bypassed by --skip-gates

// projectStatuses is the vocabulary `endless project init --status` offers.
var projectStatuses = []string{"active", "paused", "archived", "idea"}

// checkGates refuses a value the lifecycle or a vocabulary would. row is nil
// for an insert. Every problem is collected, so one refusal names them all.
func checkGates(db dbQuerier, t CorrectionTarget, row, values map[string]any) error {
	var problems []string
	str := func(col string) (string, bool) {
		v, ok := values[col]
		if !ok || v == nil {
			return "", false
		}
		return fmt.Sprint(v), true
	}
	switch t.Table {
	case "tasks":
		if phase, ok := str("phase"); ok {
			if err := ValidatePhase(phase); err != nil {
				problems = append(problems, err.Error())
			}
		}
		if status, ok := str("status"); ok {
			problems = append(problems, taskStatusGate(db, row, values, status)...)
		}
		if _, phaseSet := values["phase"]; phaseSet || values["parent_id"] != nil {
			phase, parent := effectivePhaseAndParent(row, values)
			if err := ValidateMaybeParentless(phase, parent); err != nil {
				problems = append(problems, err.Error())
			}
		}
	case "sessions":
		if state, ok := str("state"); ok {
			if err := sessionstate.Validate(sessionstate.State(state)); err != nil {
				problems = append(problems, err.Error())
			}
		}
	case "decisions":
		if status, ok := str("status"); ok && !validDecisionStatuses[status] {
			problems = append(problems, fmt.Sprintf("decision status %q is not proposed, accepted, rejected, superseded or obsolete", status))
		}
	case "task_questions":
		if status, ok := str("status"); ok {
			if err := questionstatus.Validate(status); err != nil {
				problems = append(problems, err.Error())
			}
		}
	case "task_plan_items":
		if state, ok := str("state"); ok && state != planitem.StateActive && state != planitem.StateWithdrawn {
			problems = append(problems, fmt.Sprintf("plan item state %q is not active or withdrawn", state))
		}
		if kind, ok := str("kind"); ok {
			if err := planitem.ValidateKind(kind); err != nil {
				problems = append(problems, err.Error())
			}
		}
	case "projects":
		if status, ok := str("status"); ok && !contains(projectStatuses, status) {
			problems = append(problems, fmt.Sprintf("project status %q is not %s", status,
				strings.Join(projectStatuses, ", ")))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	// The bypass is offered to the person, who is the whole audience of a
	// user-only command, and withheld from an agent, which is refused before
	// it gets this far anyway.
	return refusal.NoReport(
		fmt.Sprintf("correct: a gate refuses this change: %s", strings.Join(problems, "; ")),
		"Fix what the gate names, or retry with --skip-gates to record the correction past it").
		HumanRemedy("--skip-gates bypasses the gates, and the correction records that they were skipped")
}

// taskStatusGate is the gate on a task's status: the vocabulary, the
// transition table for the task's type, the landed→assumed rule, and plan-item
// approval for a status that reports the work.
func taskStatusGate(db dbQuerier, row, values map[string]any, status string) []string {
	var problems []string
	if err := taskstatus.Validate(status); err != nil {
		return []string{err.Error()}
	}
	if row == nil {
		return nil
	}
	current, _ := row["status"].(string)
	tt := tasktype.TaskTypeTask
	if id, ok := row["type_id"].(int64); ok {
		tt = tasktype.TaskType(id)
	}
	if typ, ok := values["type"]; ok && typ != nil {
		if parsed, err := tasktype.Parse(fmt.Sprint(typ)); err == nil {
			tt = parsed
		}
	}
	taskID, _ := row["id"].(int64)
	if err := ValidateStatusTransition(current, status, tt); err != nil {
		problems = append(problems, err.Error())
	}
	if err := ValidateLandedSettle(current, status, taskID); err != nil {
		problems = append(problems, "landed → assumed is set by the post-land reconcile, not by hand")
	}
	if contains(planitem.ReportedStatuses, status) {
		if msg := planApprovalProblem(db, taskID, tt); msg != "" {
			problems = append(problems, msg)
		}
	}
	return problems
}

// planApprovalProblem is the plan-item approval check (E-2485): a status that
// reports the work needs every active planned item approved as it stands, and
// a todo or bugfix needs an active deliverable.
func planApprovalProblem(db dbQuerier, taskID int64, tt tasktype.TaskType) string {
	var items, active, deliverables, unapproved int
	err := db.QueryRow(
		`SELECT count(*),
		        COALESCE(SUM(state = ?), 0),
		        COALESCE(SUM(state = ? AND kind = ?), 0),
		        COALESCE(SUM(CASE
		            WHEN state = ? AND origin = ? AND (approved_at IS NULL OR statement != approved_statement
		                                OR kind != approved_kind
		                                OR post_land != COALESCE(approved_post_land, 0)) THEN 1
		            ELSE 0 END), 0)
		   FROM task_plan_items WHERE task_id = ?`,
		planitem.StateActive, planitem.StateActive, planitem.KindDeliverable,
		planitem.StateActive, planitem.OriginPlanned, taskID,
	).Scan(&items, &active, &deliverables, &unapproved)
	if err != nil {
		return "could not read the task's plan items: " + err.Error()
	}
	must := planitem.MustItemize(tt.String())
	if !must && active == 0 {
		return ""
	}
	switch {
	case deliverables == 0:
		return fmt.Sprintf("E-%d has no active deliverable plan item", taskID)
	case unapproved > 0:
		return fmt.Sprintf("%d plan item(s) of E-%d are not approved as they stand (`endless task plan approve E-%d`)",
			unapproved, taskID, taskID)
	}
	return ""
}

// effectivePhaseAndParent combines the row with the values an update writes.
func effectivePhaseAndParent(row, values map[string]any) (string, *int64) {
	var phase string
	if row != nil {
		phase, _ = row["phase"].(string)
	}
	if v, ok := values["phase"].(string); ok {
		phase = v
	}
	var parent *int64
	if _, set := values["parent_id"]; set {
		if v, ok := values["parent_id"].(int64); ok && v > 0 {
			parent = &v
		}
	} else if row != nil {
		if v, ok := row["parent_id"].(int64); ok && v > 0 {
			parent = &v
		}
	}
	return phase, parent
}

// epicNote tells the user that a corrected epic status is provisional: the
// next derivation from its children can overwrite it.
func epicNote(t CorrectionTarget, row, values map[string]any) []string {
	if t.Name != "task" {
		return nil
	}
	if _, ok := values["status"]; !ok {
		return nil
	}
	isEpic := false
	if row != nil {
		if id, ok := row["type_id"].(int64); ok && tasktype.TaskType(id) == tasktype.TaskTypeEpic {
			isEpic = true
		}
	}
	if typ, ok := values["type"]; ok && typ != nil && fmt.Sprint(typ) == tasktype.TaskTypeEpic.String() {
		isEpic = true
	}
	if !isEpic {
		return nil
	}
	return []string{"This task is an epic: its status is derived from its children, so the next derivation can overwrite this correction."}
}

// mirrorScope names the records whose document mirrors a change can touch:
// a task when its content changed, a plan item's task, a decision whose
// description changed.
func mirrorScope(t CorrectionTarget, row, values map[string]any) (taskIDs, decisionIDs []int64) {
	switch t.Name {
	case "task":
		for col := range values {
			if _, ok := taskContentFields[col]; ok {
				if id, ok := row["id"].(int64); ok {
					return []int64{id}, nil
				}
			}
		}
		if values == nil { // delete: the mirrors of a removed task are left alone
			return nil, nil
		}
	case "plan_item":
		if id, ok := row["task_id"].(int64); ok {
			return []int64{id}, nil
		}
	case "decision":
		if _, ok := values["description"]; ok {
			if id, ok := row["id"].(int64); ok {
				return nil, []int64{id}
			}
		}
	}
	return nil, nil
}

// entityFor is the entity a correction of this row is recorded against: the
// row's own entity type with the id that type's events carry, or the task a
// task-scoped table belongs to.
func entityFor(db dbQuerier, t CorrectionTarget, key, row map[string]any) (EntityRef, error) {
	id := func(v any) string { return fmt.Sprint(normalizeValue(v)) }
	switch t.Name {
	case "task", "task_dep", "project", "session", "session_status", "session_tasks", "note",
		"decision", "decision_relation", "task_question":
		if t.Name == "task_dep" {
			return EntityRef{Type: t.Entity, ID: id(row["source_id"])}, nil
		}
		return EntityRef{Type: t.Entity, ID: id(key["id"])}, nil
	case "shipped_file":
		return EntityRef{Type: t.Entity, ID: id(key["path"])}, nil
	case "plan_item", "task_landing", "task_reconciliation":
		return EntityRef{Type: EntityTask, ID: id(row["task_id"])}, nil
	case "task_reconciliation_entry", "task_post_land_entry":
		var taskID int64
		if err := db.QueryRow("SELECT task_id FROM task_reconciliations WHERE id = ?",
			row["reconciliation_id"]).Scan(&taskID); err != nil {
			return EntityRef{}, refusal.NoReport(
				fmt.Sprintf("correct: reconciliation %v names no task_reconciliations row", row["reconciliation_id"]),
				"Retry with the id of a reconciliation that exists")
		}
		return EntityRef{Type: EntityTask, ID: fmt.Sprint(taskID)}, nil
	case "session_hidden_task", "session_pinned_task", "session_notice":
		return EntityRef{Type: t.Entity, ID: id(row["session_id"])}, nil
	}
	return EntityRef{}, refusal.Faultf("correct: no entity rule for target %s", t.Name)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Apply: the executor and the projector call the same function

// execLedgerRowChanged applies a change entry live.
func execLedgerRowChanged(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	return &ExecuteResult{}, applyRowChanged(db, evt)
}

// replayLedgerRowChanged applies a change entry on rebuild.
func replayLedgerRowChanged(db *sql.DB, evt *Event, result *ProjectResult) error {
	if err := applyRowChanged(db, evt); err != nil {
		return err
	}
	var p RowChangedPayload
	if json.Unmarshal(evt.Payload, &p) == nil && p.Target == "task" {
		result.TasksUpdated++
	}
	return nil
}

// applyRowChanged writes exactly what the payload says: nothing else runs. The
// column names are checked against the target's list and the table's own
// columns before any SQL is built from them, so a hand-edited ledger line
// cannot smuggle an identifier in.
func applyRowChanged(db dbQuerier, evt *Event) error {
	var p RowChangedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("events: unmarshal ledger.row_changed payload: %w", err)
	}
	t, ok := CorrectionTargetByName(p.Target)
	if !ok || t.Annotates() {
		return fmt.Errorf("events: ledger.row_changed names no table target: %q", p.Target)
	}
	shape, err := readTableShape(db, t.Table)
	if err != nil {
		return err
	}
	key, err := parseKey(t, shape, p.Key)
	if err != nil {
		return err
	}
	values := make(map[string]any, len(p.Values))
	for col, v := range p.Values {
		values[col] = normalizeValue(v)
	}
	switch p.Op {
	case RowOpUpdate:
		return applyUpdate(db, t, shape, key, values)
	case RowOpInsert:
		return applyInsert(db, t, shape, values)
	case RowOpDelete:
		return applyDelete(db, t, shape, key)
	}
	return fmt.Errorf("events: ledger.row_changed has unknown op %q", p.Op)
}

// splitTaskValues separates a task change's values into the tasks columns
// (translated to their storage form) and its content writes.
func splitTaskValues(values map[string]any) (map[string]any, []contentWrite, error) {
	cols := make(map[string]any, len(values))
	fields := make(map[string]any)
	for col, v := range values {
		switch {
		case isTaskContentField(col):
			fields[col] = v
		case col == "type":
			if v == nil {
				cols["type_id"] = nil
				continue
			}
			tt, err := tasktype.Parse(fmt.Sprint(v))
			if err != nil {
				return nil, nil, fmt.Errorf("events: ledger.row_changed task type: %w", err)
			}
			cols["type_id"] = int(tt)
		default:
			if axis, ok := rating.AxisForField(col); ok {
				cv, err := axis.ColumnValue(v)
				if err != nil {
					return nil, nil, fmt.Errorf("events: ledger.row_changed rating: %w", err)
				}
				cols[axis.Column] = cv
				continue
			}
			cols[col] = v
		}
	}
	writes, err := contentWrites(fields, "")
	if err != nil {
		return nil, nil, err
	}
	return cols, writes, nil
}

// checkedColumns refuses a column the target's list and the table do not both
// know. For the task target the list is checked at prepare and the storage
// columns here.
func checkedColumns(t CorrectionTarget, shape *tableShape, cols map[string]any) ([]string, error) {
	names := sortedColumns(cols)
	for _, col := range names {
		if _, ok := shape.Columns[col]; !ok {
			return nil, fmt.Errorf("events: ledger.row_changed names %q, not a column of %s", col, shape.Table)
		}
	}
	return names, nil
}

func applyUpdate(db dbQuerier, t CorrectionTarget, shape *tableShape, key, values map[string]any) error {
	cols := values
	var writes []contentWrite
	if t.Name == "task" {
		var err error
		if cols, writes, err = splitTaskValues(values); err != nil {
			return err
		}
	}
	names, err := checkedColumns(t, shape, cols)
	if err != nil {
		return err
	}
	if len(names) > 0 {
		set := make([]string, len(names))
		args := make([]any, 0, len(names)+len(key))
		for i, col := range names {
			set[i] = col + " = ?"
			args = append(args, cols[col])
		}
		where, keyArgs := keyWhere(t, key)
		args = append(args, keyArgs...)
		if _, err := db.Exec(fmt.Sprintf("UPDATE %s SET %s WHERE %s", shape.Table,
			strings.Join(set, ", "), where), args...); err != nil {
			return fmt.Errorf("events: ledger.row_changed update %s: %w", t.Name, err)
		}
	}
	if len(writes) > 0 {
		return writeTaskContent(db, key["id"], writes)
	}
	return nil
}

func applyInsert(db dbQuerier, t CorrectionTarget, shape *tableShape, values map[string]any) error {
	cols := values
	var writes []contentWrite
	if t.Name == "task" {
		var err error
		if cols, writes, err = splitTaskValues(values); err != nil {
			return err
		}
	}
	names, err := checkedColumns(t, shape, cols)
	if err != nil {
		return err
	}
	marks := make([]string, len(names))
	args := make([]any, len(names))
	for i, col := range names {
		marks[i] = "?"
		args[i] = cols[col]
	}
	if _, err := db.Exec(fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", shape.Table,
		strings.Join(names, ", "), strings.Join(marks, ", ")), args...); err != nil {
		return fmt.Errorf("events: ledger.row_changed insert %s: %w", t.Name, err)
	}
	if len(writes) > 0 {
		return writeTaskContent(db, cols["id"], writes)
	}
	return nil
}

func applyDelete(db dbQuerier, t CorrectionTarget, shape *tableShape, key map[string]any) error {
	where, args := keyWhere(t, key)
	q := fmt.Sprintf("DELETE FROM %s WHERE %s", t.Table, where)
	if shape.SoftRemoves() {
		// A table with a removed column retains its rows (ED-1547): the row is
		// marked removed = 1, never deleted, so its id is never minted again.
		q = fmt.Sprintf("UPDATE %s SET removed = 1 WHERE %s", t.Table, where)
	}
	if _, err := db.Exec(q, args...); err != nil {
		return fmt.Errorf("events: ledger.row_changed delete %s: %w", t.Name, err)
	}
	return nil
}

// ExecAllAndCommit applies several events in the open transaction, in order,
// then commits and releases the lock. A correction run is one change entry or
// more followed by the correction entry, and all of it lands or none does.
func (l *LockedEmit) ExecAllAndCommit(evts []*Event) error {
	for _, evt := range evts {
		if _, err := dispatch(l.DB, evt, nil); err != nil {
			l.Rollback()
			return err
		}
	}
	if _, err := l.DB.Exec("COMMIT"); err != nil {
		return fmt.Errorf("events: commit: %w", err)
	}
	return nil
}

// TaskContentSlugs is the content vocabulary the task target lists, for help.
func TaskContentSlugs() []string { return taskcontent.Slugs() }
