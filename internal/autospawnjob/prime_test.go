package autospawnjob

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/jobs"
)

// primeable is a task the prime job should pick: a plan was attached, nobody
// has started it, and it is current work. Ratings and type do not matter here —
// priming reads in, it does not do the work.
func primeable(id int64) taskSpec {
	return taskSpec{
		id: id, project: projA, status: "submitted", phase: "now", typeID: 4,
		created: "2026-09-01T00:00:00", plan: "# Plan\nDo it.",
	}
}

func seedPrimeable(t *testing.T, db *sql.DB, s taskSpec) {
	t.Helper()
	seedTask(t, db, s)
	mustExec(t, db, `UPDATE tasks SET prime_requested = 1, updated_at = ? WHERE id = ?`, s.created, s.id)
}

// TestPrimeSelect_EachConditionExcludes is the package doc's list, one case per
// condition: the same primeable task with exactly one thing wrong.
func TestPrimeSelect_EachConditionExcludes(t *testing.T) {
	cases := []struct {
		name  string
		tweak func(*taskSpec)
		after func(t *testing.T, db *sql.DB)
	}{
		{"1. no plan attach was recorded", nil, func(t *testing.T, db *sql.DB) {
			mustExec(t, db, `UPDATE tasks SET prime_requested = 0 WHERE id = 100`)
		}},
		{"1. the plan was cleared since", func(s *taskSpec) { s.plan = "" }, nil},
		{"2. underway", func(s *taskSpec) { s.status = "underway" }, nil},
		{"2. unverified", func(s *taskSpec) { s.status = "unverified" }, nil},
		{"3. phase is next", func(s *taskSpec) { s.phase = "next" }, nil},
		{"4. project not opted in", func(s *taskSpec) { s.project = projB }, nil},
		{"5. blocked by a non-terminal task", nil, func(t *testing.T, db *sql.DB) {
			seedTask(t, db, taskSpec{id: 900, project: projA, status: "unverified", phase: "later", typeID: 1, created: "2026-01-01T00:00:00"})
			mustExec(t, db, `INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
			             VALUES ('task', 900, 'task', 100, 'blocks')`)
		}},
		{"6. an open question", nil, func(t *testing.T, db *sql.DB) {
			mustExec(t, db, `INSERT INTO task_questions (task_id, series, question, status) VALUES (100, 1, 'which?', 'open')`)
		}},
		{"7. a session already bound (the primed session itself)", nil, func(t *testing.T, db *sql.DB) {
			mustExec(t, db, `INSERT INTO sessions (session_id, state, task_id) VALUES ('primed', 'primed', 100)`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			dirA, _ := seedProjects(t, db)
			s := primeable(100)
			if tc.tweak != nil {
				tc.tweak(&s)
			}
			seedPrimeable(t, db, s)
			if tc.after != nil {
				tc.after(t, db)
			}
			pick, skip, err := selectPrimeCandidate(db, policies(map[string]projectPolicy{
				dirA: {enabled: true, cap: 3},
			}))
			if err != nil {
				t.Fatalf("selectPrimeCandidate: %v", err)
			}
			if pick.taskID != 0 {
				t.Fatalf("picked E-%d, want nothing", pick.taskID)
			}
			if skip != "nothing to prime" {
				t.Errorf("skip = %q, want %q", skip, "nothing to prime")
			}
		})
	}
}

// TestPrimeSelect_Picks is the positive control, for both pre-work statuses.
func TestPrimeSelect_Picks(t *testing.T) {
	for _, status := range []string{"submitted", "ready"} {
		db := newTestDB(t)
		dirA, _ := seedProjects(t, db)
		s := primeable(100)
		s.status = status
		seedPrimeable(t, db, s)
		pick, skip, err := selectPrimeCandidate(db, policies(map[string]projectPolicy{
			dirA: {enabled: true, cap: 3},
		}))
		if err != nil || skip != "" {
			t.Fatalf("%s: err=%v skip=%q", status, err, skip)
		}
		if pick.taskID != 100 || pick.projectPath != dirA {
			t.Errorf("%s: pick = %+v, want E-100 in %s", status, pick, dirA)
		}
	}
}

// TestPrimeSelect_Cap: live sessions on unstarted tasks count against the cap;
// an ended one, or one whose task was started, does not.
func TestPrimeSelect_Cap(t *testing.T) {
	db := newTestDB(t)
	dirA, _ := seedProjects(t, db)
	seedPrimeable(t, db, primeable(100))
	for i, st := range []struct{ status, state string }{
		{"submitted", "primed"},
		{"ready", "working"},
		{"submitted", "ended"},  // not live
		{"underway", "working"}, // started: the resume claimed it
	} {
		id := int64(200 + i)
		seedTask(t, db, taskSpec{id: id, project: projA, status: st.status, phase: "now", typeID: 1, created: "2026-09-01T00:00:00"})
		mustExec(t, db, `INSERT INTO sessions (session_id, state, task_id) VALUES (?, ?, ?)`,
			"s"+string(rune('a'+i)), st.state, id)
	}
	n, err := outstandingPrimed(db, projA)
	if err != nil || n != 2 {
		t.Fatalf("outstandingPrimed = %d, %v; want 2", n, err)
	}
	_, skip, err := selectPrimeCandidate(db, policies(map[string]projectPolicy{dirA: {enabled: true, cap: 2}}))
	if err != nil || !strings.Contains(skip, "prime cap") {
		t.Errorf("at cap: skip = %q, err = %v", skip, err)
	}
	pick, _, _ := selectPrimeCandidate(db, policies(map[string]projectPolicy{dirA: {enabled: true, cap: 3}}))
	if pick.taskID != 100 {
		t.Errorf("under cap: pick = E-%d, want E-100", pick.taskID)
	}
}

func TestPrimeSelect_NobodyOptedIn(t *testing.T) {
	db := newTestDB(t)
	seedProjects(t, db)
	seedPrimeable(t, db, primeable(100))
	_, skip, err := selectPrimeCandidate(db, policies(nil))
	if err != nil || !strings.Contains(skip, "prime.enabled") {
		t.Errorf("skip = %q, err = %v", skip, err)
	}
}

func TestLoadPrimePolicy_DefaultsAndOptIn(t *testing.T) {
	dir := realDir(t)
	p, err := loadPrimePolicy(dir)
	if err != nil || p.enabled {
		t.Fatalf("no config: %+v, %v; want disabled", p, err)
	}
	writeFile(t, filepath.Join(dir, ".endless", "config.json"), `{"prime":{"enabled":true}}`)
	p, err = loadPrimePolicy(dir)
	if err != nil || !p.enabled || p.cap != 3 {
		t.Errorf("opted in: %+v, %v; want enabled with the default cap 3", p, err)
	}
}

func TestPrimeArgs(t *testing.T) {
	if got, want := primeArgs(42, "$5", "last"), []string{"--no-session", "task", "prime", "E-42", "--auto", "--placement", "last", "--target-session", "$5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("with target: %q, want %q", got, want)
	}
}

func TestPrimeRegistered(t *testing.T) {
	if _, ok := jobs.Lookup(PrimeJobName); !ok {
		t.Fatalf("%q is not registered", PrimeJobName)
	}
}
