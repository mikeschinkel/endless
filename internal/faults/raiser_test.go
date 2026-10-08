package faults_test

import (
	"database/sql"
	"testing"

	"github.com/mikeschinkel/endless/internal/faults"
)

// bindRaiser rebinds the store already built by newBoundStore with a raiser
// resolver that answers resolved for every field the producer left 0.
func bindRaiser(t *testing.T, db *sql.DB, logDir string, resolved faults.Raiser) {
	t.Helper()
	faults.Bind(
		func() (*sql.DB, error) { return db, nil },
		func() string { return logDir },
		nil,
		func(explicit faults.Raiser) faults.Raiser {
			out := resolved
			if explicit.TaskID != 0 {
				out.TaskID = explicit.TaskID
			}
			if explicit.SessionID != 0 {
				out.SessionID = explicit.SessionID
			}
			return out
		},
	)
}

// shared is the fault several raisers hit identically.
func shared() faults.Fault {
	return faults.Fault{
		Code:        faults.ErrCodeJobFailed,
		Source:      "job:evaluate",
		Fingerprint: "shared",
		Summary:     "job \"evaluate\" failed",
	}
}

func onlyIncident(t *testing.T) faults.Incident {
	t.Helper()
	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("got %d incidents, want exactly 1", len(incidents))
	}
	return incidents[0]
}

func sourcesOf(t *testing.T, id int64) []faults.Source {
	t.Helper()
	sources, err := faults.Sources(id)
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	return sources
}

func TestRecord_AttributesTheResolvedRaiser(t *testing.T) {
	db, logDir := newBoundStore(t)
	bindRaiser(t, db, logDir, faults.Raiser{TaskID: 40, SessionID: 7})

	faults.Record(shared())

	incident := onlyIncident(t)
	if incident.TaskID != 40 || incident.SessionID != 7 {
		t.Errorf("incident raiser = E-%d/ES-%d, want E-40/ES-7", incident.TaskID, incident.SessionID)
	}
	if incident.Raisers != 1 {
		t.Errorf("raisers = %d, want 1", incident.Raisers)
	}
	sources := sourcesOf(t, incident.ID)
	if len(sources) != 1 || sources[0].TaskID != 40 || sources[0].SessionID != 7 || sources[0].Occurrences != 1 {
		t.Errorf("sources = %+v, want one E-40/ES-7 row with 1 occurrence", sources)
	}

	lines := readLog(t, logDir)
	if len(lines) != 1 || lines[0].TaskID != 40 || lines[0].SessionID != 7 {
		t.Errorf("detail lines = %+v, want one carrying E-40/ES-7", lines)
	}
}

func TestRecord_ExplicitRaiserOverridesTheResolvedOne(t *testing.T) {
	db, logDir := newBoundStore(t)
	bindRaiser(t, db, logDir, faults.Raiser{TaskID: 40, SessionID: 7})

	f := shared()
	f.TaskID = 99
	faults.Record(f)

	incident := onlyIncident(t)
	if incident.TaskID != 99 || incident.SessionID != 7 {
		t.Errorf("incident raiser = E-%d/ES-%d, want E-99 (explicit) / ES-7 (resolved)",
			incident.TaskID, incident.SessionID)
	}
}

func TestRecord_ExplicitRaiserWinsOverAResolverThatIgnoresIt(t *testing.T) {
	db, logDir := newBoundStore(t)
	faults.Bind(
		func() (*sql.DB, error) { return db, nil },
		func() string { return logDir },
		nil,
		func(faults.Raiser) faults.Raiser { return faults.Raiser{TaskID: 1, SessionID: 2} },
	)

	f := shared()
	f.TaskID, f.SessionID = 99, 98
	faults.Record(f)

	incident := onlyIncident(t)
	if incident.TaskID != 99 || incident.SessionID != 98 {
		t.Errorf("incident raiser = E-%d/ES-%d, want the producer's E-99/ES-98",
			incident.TaskID, incident.SessionID)
	}
}

func TestRecord_ASecondRaiserUpdatesTheLatestAndAddsASource(t *testing.T) {
	db, logDir := newBoundStore(t)

	bindRaiser(t, db, logDir, faults.Raiser{TaskID: 40, SessionID: 7})
	faults.Record(shared())
	bindRaiser(t, db, logDir, faults.Raiser{TaskID: 41, SessionID: 8})
	faults.Record(shared())

	incident := onlyIncident(t)
	if incident.Occurrences != 2 {
		t.Errorf("occurrences = %d, want 2", incident.Occurrences)
	}
	if incident.TaskID != 41 || incident.SessionID != 8 {
		t.Errorf("latest raiser = E-%d/ES-%d, want E-41/ES-8", incident.TaskID, incident.SessionID)
	}
	if incident.Raisers != 2 {
		t.Errorf("raisers = %d, want 2", incident.Raisers)
	}
	sources := sourcesOf(t, incident.ID)
	if len(sources) != 2 {
		t.Fatalf("sources = %+v, want 2 rows", sources)
	}
	for _, s := range sources {
		if s.Occurrences != 1 {
			t.Errorf("source E-%d/ES-%d occurrences = %d, want 1", s.TaskID, s.SessionID, s.Occurrences)
		}
	}
}

func TestRecord_ARepeatFromTheSameRaiserOnlyBumpsItsRow(t *testing.T) {
	db, logDir := newBoundStore(t)
	bindRaiser(t, db, logDir, faults.Raiser{TaskID: 40, SessionID: 7})

	for range 3 {
		faults.Record(shared())
	}

	incident := onlyIncident(t)
	sources := sourcesOf(t, incident.ID)
	if len(sources) != 1 || sources[0].Occurrences != 3 {
		t.Errorf("sources = %+v, want one row with 3 occurrences", sources)
	}
	if incident.Raisers != 1 {
		t.Errorf("raisers = %d, want 1", incident.Raisers)
	}
}

func TestRecord_UnattributedOccurrencesShareOneSource(t *testing.T) {
	db, logDir := newBoundStore(t)

	for range 3 {
		faults.Record(shared())
	}

	incident := onlyIncident(t)
	if incident.TaskID != 0 || incident.SessionID != 0 {
		t.Errorf("raiser = E-%d/ES-%d, want none", incident.TaskID, incident.SessionID)
	}
	var nulls int
	if err := db.QueryRow(
		`SELECT count(*) FROM errors WHERE task_id IS NULL AND session_id IS NULL`,
	).Scan(&nulls); err != nil || nulls != 1 {
		t.Errorf("unattributed incident stored %d NULL-raiser rows (err %v), want 1 — 0 must be NULL", nulls, err)
	}
	sources := sourcesOf(t, incident.ID)
	if len(sources) != 1 || sources[0].Occurrences != 3 {
		t.Errorf("sources = %+v, want one shared row with 3 occurrences", sources)
	}
	for _, line := range readLog(t, logDir) {
		if line.TaskID != 0 || line.SessionID != 0 {
			t.Errorf("detail line names E-%d/ES-%d, want neither", line.TaskID, line.SessionID)
		}
	}
}
