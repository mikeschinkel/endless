// Package rating defines the two per-task rating axes, Complexity and Risk
// (ED-1539), the source of truth for tasks.complexity_id and tasks.risk_id (per
// ED-1506: const-in-code is the source of truth, the complexity_levels and
// risk_levels SQL tables mirror it for FK enforcement and queryability). It
// lives outside internal/events and internal/monitor so both can depend on it
// without a cycle.
//
// # The two axes
//
//   - Complexity — how much human-AI interaction is needed to nail down the
//     specifics.
//   - Risk — the blast radius if the work is wrong.
//
// They are independent: low-complexity work can still be high-risk, which is
// why they are two types rather than one scalar. Keeping them distinct Go types
// means a Risk can never be passed where a Complexity is expected.
//
// # The scale
//
// Both axes share one scale: low=1, medium=3, high=5. Ids 2 and 4 are
// deliberately unseeded so medium-low and medium-high can be added later
// without renumbering — ids are persisted in tasks, so renumbering would
// reclassify live rows. Adding a level = add a constant here + a seed row in
// internal/schema/schema.sql and internal/schema/seeds.sql. VerifyIntegrity
// fails closed on drift.
//
// # Who sets them
//
// The agent proposes both at submit and the user ratifies them at approve
// (ED-1538). A rating never moves status.
package rating

import (
	"database/sql"
	"fmt"
	"strings"
)

// Level is the shared scale both axes rate on. Callers that know which axis
// they hold use Complexity or Risk; Level is for code generic over both.
type Level int

const (
	Low    Level = 1
	Medium Level = 3
	High   Level = 5
)

// Levels returns the seeded levels in id order.
func Levels() []Level {
	return []Level{Low, Medium, High}
}

// String returns the lowercase machine slug.
func (l Level) String() string {
	switch l {
	case Low:
		return "low"
	case Medium:
		return "medium"
	case High:
		return "high"
	default:
		return fmt.Sprintf("Level(%d)", int(l))
	}
}

// Label returns the human display string.
func (l Level) Label() string {
	switch l {
	case Low:
		return "Low"
	case Medium:
		return "Medium"
	case High:
		return "High"
	default:
		return ""
	}
}

// Valid reports whether l is a seeded level.
func (l Level) Valid() bool {
	switch l {
	case Low, Medium, High:
		return true
	default:
		return false
	}
}

// ParseLevel converts a slug to a Level. Case-insensitive, because the slug
// arrives from CLI flags and from a model's reply.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return Low, nil
	case "medium":
		return Medium, nil
	case "high":
		return High, nil
	default:
		return 0, fmt.Errorf("rating: invalid level %q (valid: low, medium, high)", s)
	}
}

// Complexity is how much human-AI interaction is needed to nail down the
// specifics of a task.
type Complexity Level

// Risk is the blast radius if a task's work is wrong.
type Risk Level

func (c Complexity) String() string { return Level(c).String() }
func (c Complexity) Label() string  { return Level(c).Label() }
func (r Risk) String() string       { return Level(r).String() }
func (r Risk) Label() string        { return Level(r).Label() }

// ParseComplexity converts a slug to a Complexity.
func ParseComplexity(s string) (Complexity, error) {
	l, err := ParseLevel(s)
	if err != nil {
		return 0, fmt.Errorf("complexity: %w", err)
	}
	return Complexity(l), nil
}

// ParseRisk converts a slug to a Risk.
func ParseRisk(s string) (Risk, error) {
	l, err := ParseLevel(s)
	if err != nil {
		return 0, fmt.Errorf("risk: %w", err)
	}
	return Risk(l), nil
}

// Axis names one of the two rating axes: its task field name (the key a
// `task.fields_updated` payload carries), its tasks column, and its mirror
// table. The executor, the projector and the integrity check all iterate
// Axes() so the two axes cannot drift apart in one place and not the other.
type Axis struct {
	Field  string
	Column string
	Table  string
}

var (
	ComplexityAxis = Axis{Field: "complexity", Column: "complexity_id", Table: "complexity_levels"}
	RiskAxis       = Axis{Field: "risk", Column: "risk_id", Table: "risk_levels"}
)

// Axes returns both axes, complexity first.
func Axes() []Axis {
	return []Axis{ComplexityAxis, RiskAxis}
}

// AxisForField returns the axis whose task field name is field.
func AxisForField(field string) (Axis, bool) {
	for _, a := range Axes() {
		if a.Field == field {
			return a, true
		}
	}
	return Axis{}, false
}

// ColumnValue converts a payload value for this axis into the value to store
// in its column: a slug becomes its level id, and nil or "" or "none" clears
// the rating (NULL). Anything else is an error, so an unknown slug never
// reaches the database.
func (a Axis) ColumnValue(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("%s: expected a level slug, got %T", a.Field, v)
	}
	if s == "" || strings.EqualFold(s, "none") {
		return nil, nil
	}
	l, err := ParseLevel(s)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", a.Field, err)
	}
	return int(l), nil
}

// VerifyIntegrity asserts that both mirror tables match Levels(). Runs once at
// startup (from monitor.DB() after the schema applies). Returns an error on any
// drift: a level with no matching row, a slug or label mismatch, or a row whose
// id matches no level. Callers are expected to hard-fail the process.
func VerifyIntegrity(db *sql.DB) error {
	for _, a := range Axes() {
		if err := verifyTable(db, a.Table); err != nil {
			return err
		}
	}
	return nil
}

func verifyTable(db *sql.DB, table string) error {
	type row struct {
		slug  string
		label string
	}
	rows, err := db.Query("SELECT id, slug, label FROM " + table)
	if err != nil {
		return fmt.Errorf("rating: query %s: %w", table, err)
	}
	defer rows.Close()

	byID := make(map[int]row)
	for rows.Next() {
		var id int
		var r row
		if err := rows.Scan(&id, &r.slug, &r.label); err != nil {
			return fmt.Errorf("rating: scan %s row: %w", table, err)
		}
		byID[id] = r
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rating: iterate %s: %w", table, err)
	}

	for _, l := range Levels() {
		r, ok := byID[int(l)]
		if !ok {
			return fmt.Errorf("rating: level %s (id=%d) missing from %s", l, int(l), table)
		}
		if r.slug != l.String() {
			return fmt.Errorf("rating: %s id=%d slug mismatch: enum=%q, table=%q",
				table, int(l), l.String(), r.slug)
		}
		if r.label != l.Label() {
			return fmt.Errorf("rating: %s id=%d label mismatch: enum=%q, table=%q",
				table, int(l), l.Label(), r.label)
		}
		delete(byID, int(l))
	}
	for id, r := range byID {
		return fmt.Errorf("rating: %s row id=%d slug=%q has no matching level", table, id, r.slug)
	}
	return nil
}
