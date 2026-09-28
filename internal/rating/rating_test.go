package rating_test

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/rating"
)

func TestLevels_AreLowMediumHighWithGaps(t *testing.T) {
	want := map[rating.Level]int{rating.Low: 1, rating.Medium: 3, rating.High: 5}
	got := rating.Levels()
	if len(got) != len(want) {
		t.Fatalf("Levels() = %v, want %d levels", got, len(want))
	}
	for _, l := range got {
		if int(l) != want[l] {
			t.Errorf("%s id = %d, want %d", l, int(l), want[l])
		}
	}
}

func TestParse_RoundTripsEveryLevel(t *testing.T) {
	for _, l := range rating.Levels() {
		c, err := rating.ParseComplexity(l.String())
		if err != nil || rating.Level(c) != l {
			t.Errorf("ParseComplexity(%q) = %v, %v", l.String(), c, err)
		}
		r, err := rating.ParseRisk(strings.ToUpper(l.String()))
		if err != nil || rating.Level(r) != l {
			t.Errorf("ParseRisk(%q) = %v, %v", strings.ToUpper(l.String()), r, err)
		}
	}
}

func TestParse_RejectsUnknown(t *testing.T) {
	for _, s := range []string{"", "none", "2", "med", "critical"} {
		if _, err := rating.ParseLevel(s); err == nil {
			t.Errorf("ParseLevel(%q) accepted an invalid level", s)
		}
	}
	if _, err := rating.ParseRisk("extreme"); err == nil || !strings.Contains(err.Error(), "risk:") {
		t.Errorf("ParseRisk error should name its axis, got %v", err)
	}
}

func TestAxis_ColumnValue(t *testing.T) {
	a := rating.ComplexityAxis
	cases := []struct {
		in   any
		want any
	}{
		{nil, nil}, {"", nil}, {"none", nil}, {"NONE", nil},
		{"low", 1}, {"medium", 3}, {"high", 5},
	}
	for _, c := range cases {
		got, err := a.ColumnValue(c.in)
		if err != nil {
			t.Errorf("ColumnValue(%v) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ColumnValue(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []any{"bogus", 3, 3.0} {
		if _, err := a.ColumnValue(bad); err == nil {
			t.Errorf("ColumnValue(%v) accepted an invalid value", bad)
		}
	}
}

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, a := range rating.Axes() {
		if _, err := db.Exec(`CREATE TABLE ` + a.Table + ` (id INTEGER PRIMARY KEY, slug TEXT UNIQUE NOT NULL, label TEXT NOT NULL)`); err != nil {
			t.Fatalf("create %s: %v", a.Table, err)
		}
	}
	return db
}

func seed(t *testing.T, db *sql.DB, table, values string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO ` + table + ` (id, slug, label) VALUES ` + values); err != nil {
		t.Fatalf("seed %s: %v", table, err)
	}
}

const aligned = `(1, 'low', 'Low'), (3, 'medium', 'Medium'), (5, 'high', 'High')`

func TestVerifyIntegrity_OK(t *testing.T) {
	db := newDB(t)
	seed(t, db, "complexity_levels", aligned)
	seed(t, db, "risk_levels", aligned)
	if err := rating.VerifyIntegrity(db); err != nil {
		t.Errorf("VerifyIntegrity on aligned tables: %v", err)
	}
}

func TestVerifyIntegrity_Drift(t *testing.T) {
	cases := map[string]struct{ risk, want string }{
		"missing": {`(1, 'low', 'Low'), (5, 'high', 'High')`, "missing from risk_levels"},
		"slug":    {`(1, 'lo', 'Low'), (3, 'medium', 'Medium'), (5, 'high', 'High')`, "slug mismatch"},
		"label":   {`(1, 'low', 'Lo'), (3, 'medium', 'Medium'), (5, 'high', 'High')`, "label mismatch"},
		"rogue":   {aligned + `, (2, 'medium-low', 'Medium-low')`, "no matching level"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			db := newDB(t)
			seed(t, db, "complexity_levels", aligned)
			seed(t, db, "risk_levels", c.risk)
			err := rating.VerifyIntegrity(db)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}
