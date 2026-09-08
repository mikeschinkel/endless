package projectstatuscmd

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/faultbadge"
	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

// The board's badge counts THIS project's open incidents plus the machine-level
// ones no project could be attributed to (E-1960) — never another project's.
//
// The board is scoped to one project in every other respect, so a badge counting
// the whole machine would be the one line on the frame reporting on work the
// rows above it do not show. `session status` makes the opposite call, and its
// own suite pins that; the two together are the whole scoping contract.

// bindFaultStoreWithProjects points the faults package at a throwaway DB holding
// two registered projects, and returns their ids.
//
// A near-copy of sessionstatuscmd's helper, deliberately: these tests assert the
// scope reaches the BOARD FRAME, which is a property of render. Sharing a helper
// across the package boundary would couple two suites that have to be able to
// fail independently.
func bindFaultStoreWithProjects(t *testing.T) (alpha, beta int64) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("close db: %v", cerr)
		}
	})
	if _, err = db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err = db.Exec(
		`INSERT INTO projects (id, name, path) VALUES
		     (1, 'alpha', '~/Projects/alpha'), (2, 'beta', '~/Projects/beta')`,
	); err != nil {
		t.Fatalf("seed projects: %v", err)
	}

	logDir := t.TempDir()
	bind := func(id int64) {
		faults.Bind(
			func() (*sql.DB, error) { return db, nil },
			func() string { return logDir },
			func(explicit int64) (int64, string) {
				if explicit != 0 {
					return explicit, ""
				}
				return id, ""
			},
		)
	}
	t.Cleanup(func() { faults.Bind(nil, nil, nil) })

	// Two projects fail, and so does the machine itself.
	bind(1)
	faults.Record(faults.Fault{
		Code: faults.ErrCodeJobFailed, Source: "job:a", Summary: `alpha's job failed`,
	})
	bind(2)
	faults.Record(faults.Fault{
		Code: faults.ErrCodeJobPanicked, Source: "job:b", Summary: `beta's job panicked`,
	})
	bind(0)
	faults.Record(faults.Fault{
		Code: faults.ErrCodeJobScheduling, Source: "jobs", Summary: "the runner could not open the database",
	})

	return 1, 2
}

// boardRows is the minimal row set that exercises the normal (non-empty) render
// path, so the badge is asserted where it will actually be seen.
func boardRows() []monitor.ProjectStatusRow {
	return []monitor.ProjectStatusRow{taskRow(1, "unverified", 0)}
}

func TestBoardBadge_CountsOnlyThisProjectAndTheUnattributed(t *testing.T) {
	alpha, _ := bindFaultStoreWithProjects(t)

	var b strings.Builder
	render(&b, "alpha", boardRows(), 10, 0, 120, false, now, faults.ProjectScope(alpha))
	out := b.String()

	if !strings.Contains(out, faultbadge.Hint) {
		t.Fatalf("no badge on a board with open incidents:\n%s", out)
	}
	// alpha's own fault is a warning; the unattributed one is a warning too. Beta's
	// is the only ERROR, so the severity chip is the tell: an ERROR chip here means
	// the board counted a project it has no business counting.
	if strings.Contains(out, "ERROR") {
		t.Errorf("alpha's board badged beta's error:\n%s", out)
	}
	if !strings.Contains(out, "2 warnings") {
		t.Errorf("alpha's board did not count its own fault plus the unattributed one:\n%s", out)
	}
}

func TestBoardBadge_MachineWideScopeSeesEverything(t *testing.T) {
	bindFaultStoreWithProjects(t)

	var b strings.Builder
	render(&b, "alpha", boardRows(), 10, 0, 120, false, now, faults.AllProjects)
	out := b.String()

	if !strings.Contains(out, "ERROR") {
		t.Errorf("AllProjects did not see beta's error — the scope is not being applied:\n%s", out)
	}
}
