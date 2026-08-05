package faults_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/schema"
)

// newBoundStore builds an in-memory DB carrying the real schema, points the
// faults package at it, and routes the detail log into a temp dir. Returns the
// DB so tests can assert raw table state rather than only the read API's view.
func newBoundStore(t *testing.T) (*sql.DB, string) {
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

	logDir := t.TempDir()
	faults.Bind(
		func() (*sql.DB, error) { return db, nil },
		func() string { return logDir },
	)
	t.Cleanup(func() { faults.Bind(nil, nil) })

	return db, logDir
}

func TestRecord_OpensAnIncident(t *testing.T) {
	db, _ := newBoundStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobFailed,
		Source:  "job:evaluate",
		Summary: "job \"evaluate\" failed: upstream unavailable",
		Detail:  "the full stack, or command output, or both",
	})

	incidents, err := faults.List(false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("recorded %d incidents, want 1", len(incidents))
	}

	incident := incidents[0]
	if incident.Code != "ERR-0001" {
		t.Errorf("code = %s, want ERR-0001", incident.Code)
	}
	if incident.Severity != faults.SeverityWarning {
		t.Errorf("severity = %s, want warning (it comes from the CODE, not the call site)", incident.Severity)
	}
	if incident.Source != "job:evaluate" {
		t.Errorf("source = %s, want job:evaluate", incident.Source)
	}
	if incident.Occurrences != 1 {
		t.Errorf("occurrences = %d, want 1", incident.Occurrences)
	}
	if incident.Fingerprint == "" {
		t.Error("fingerprint is empty; it should have been derived from the summary")
	}
	if incident.ClearedAt != "" {
		t.Errorf("cleared_at = %q, want empty on a new incident", incident.ClearedAt)
	}

	// The DB holds the INDEX only — the long detail must not be in the table.
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM errors`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("errors table has %d rows, want 1", count)
	}
}

func TestRecord_RepeatsBumpTheOpenIncidentInPlace(t *testing.T) {
	db, _ := newBoundStore(t)

	fault := faults.Fault{
		Code:    faults.ErrCodeJobFailed,
		Source:  "job:evaluate",
		Summary: "job \"evaluate\" failed: upstream unavailable",
	}
	faults.Record(fault)
	faults.Record(fault)
	faults.Record(fault)

	// Failing 100 times is a different signal from failing once, so the count is
	// kept — but as ONE row, so a flapping job cannot flood the table.
	var rows int
	var occurrences int
	if err := db.QueryRow(`SELECT count(*) FROM errors`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Errorf("errors table has %d rows, want 1 (repeats must dedupe in place)", rows)
	}
	if err := db.QueryRow(`SELECT occurrences FROM errors`).Scan(&occurrences); err != nil {
		t.Fatalf("read occurrences: %v", err)
	}
	if occurrences != 3 {
		t.Errorf("occurrences = %d, want 3", occurrences)
	}
}

func TestRecord_RecurrenceAfterClearingOpensANewIncident(t *testing.T) {
	db, _ := newBoundStore(t)

	fault := faults.Fault{
		Code:    faults.ErrCodeJobFailed,
		Source:  "job:evaluate",
		Summary: "job \"evaluate\" failed: upstream unavailable",
	}
	faults.Record(fault)
	faults.Record(fault)

	cleared, err := faults.Clear(nil, "tester")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared != 1 {
		t.Fatalf("cleared %d incidents, want 1", cleared)
	}

	// The same fault comes back a week later. That is a DIFFERENT signal from
	// one that never went away — it is how a regression's arrival is pinpointed
	// — so it must open a new incident, not resurrect the cleared one.
	faults.Record(fault)

	var rows int
	if err = db.QueryRow(`SELECT count(*) FROM errors`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 2 {
		t.Errorf("errors table has %d rows, want 2 (recurrence opens a new incident)", rows)
	}

	open, err := faults.List(false, 0)
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("%d open incidents, want 1", len(open))
	}
	if open[0].Occurrences != 1 {
		t.Errorf("new incident occurrences = %d, want 1 (it must not inherit the cleared count)", open[0].Occurrences)
	}

	all, err := faults.List(true, 0)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("%d total incidents, want 2", len(all))
	}

	// The cleared row is immutable history: its own count and attribution stay.
	var clearedRow faults.Incident
	for _, incident := range all {
		if incident.ClearedAt != "" {
			clearedRow = incident
		}
	}
	if clearedRow.Occurrences != 2 {
		t.Errorf("cleared incident occurrences = %d, want 2 (history must be preserved)", clearedRow.Occurrences)
	}
	if clearedRow.ClearedBy != "tester" {
		t.Errorf("cleared_by = %q, want tester", clearedRow.ClearedBy)
	}
}

func TestRecord_SuccessDoesNotAutoResolveAnIncident(t *testing.T) {
	_, _ = newBoundStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobFailed,
		Source:  "job:flaky",
		Summary: "job \"flaky\" failed intermittently",
	})

	// Nothing in the API clears an incident except an explicit Clear. This is
	// deliberate: an intermittent fault that healed itself out of view would
	// never get fixed, so every error stays visible until a human dismisses it.
	incidents, err := faults.List(false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(incidents) != 1 {
		t.Errorf("%d open incidents, want the fault to remain open until cleared", len(incidents))
	}
}

func TestDetails_CarryEveryOccurrenceWithItsFullCapture(t *testing.T) {
	_, logDir := newBoundStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobPanicked,
		Source:  "job:exploding",
		Summary: "job \"exploding\" panicked",
		Detail:  "first occurrence stack",
		Fields:  map[string]any{"job": "exploding", "attempt": 1},
	})
	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobPanicked,
		Source:  "job:exploding",
		Summary: "job \"exploding\" panicked",
		Detail:  "second occurrence stack",
		Fields:  map[string]any{"job": "exploding", "attempt": 2},
	})

	incidents, err := faults.List(false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("%d incidents, want 1", len(incidents))
	}

	details, err := faults.Details(incidents[0].ID)
	if err != nil {
		t.Fatalf("details: %v", err)
	}
	// One table row, but BOTH captures retained — the whole point of splitting
	// the index from the detail log.
	if len(details) != 2 {
		t.Fatalf("%d logged occurrences, want 2", len(details))
	}
	if details[0].Detail != "first occurrence stack" {
		t.Errorf("first detail = %q", details[0].Detail)
	}
	if details[1].Detail != "second occurrence stack" {
		t.Errorf("second detail = %q", details[1].Detail)
	}
	if details[0].Occurrence != 1 || details[1].Occurrence != 2 {
		t.Errorf("occurrence numbers = %d, %d; want 1, 2", details[0].Occurrence, details[1].Occurrence)
	}

	// The log is a plain JSONL file beside user-machine.jsonl.
	path := filepath.Join(logDir, "errors.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; lines != 2 {
		t.Errorf("detail log has %d lines, want 2", lines)
	}
}

func TestOpen_RanksErrorAboveWarningAndCountsEach(t *testing.T) {
	_, _ = newBoundStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobFailed, // warning
		Source:  "job:a",
		Summary: "job a failed",
	})
	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobScheduling, // warning
		Source:  "job:b",
		Summary: "job b could not be scheduled",
	})
	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobPanicked, // error
		Source:  "job:c",
		Summary: "job c panicked",
	})

	overview, err := faults.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if overview.Total != 3 {
		t.Errorf("total = %d, want 3", overview.Total)
	}
	if overview.Counts[faults.SeverityWarning] != 2 {
		t.Errorf("warnings = %d, want 2", overview.Counts[faults.SeverityWarning])
	}
	if overview.Counts[faults.SeverityError] != 1 {
		t.Errorf("errors = %d, want 1", overview.Counts[faults.SeverityError])
	}
	if overview.Max != faults.SeverityError {
		t.Errorf("max severity = %s, want error to outrank warning", overview.Max)
	}
	if overview.Latest == nil {
		t.Fatal("Latest is nil, want the most recently seen incident")
	}
}

func TestOpen_IsEmptyWhenEverythingIsCleared(t *testing.T) {
	_, _ = newBoundStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobFailed,
		Source:  "job:a",
		Summary: "job a failed",
	})
	if _, err := faults.Clear(nil, "tester"); err != nil {
		t.Fatalf("clear: %v", err)
	}

	overview, err := faults.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if overview.Total != 0 {
		t.Errorf("total = %d, want 0 once every incident is cleared", overview.Total)
	}
	if overview.Max != "" {
		t.Errorf("max = %q, want empty when nothing is open", overview.Max)
	}
}

func TestRecord_IsASilentNoOpWhenUnbound(t *testing.T) {
	faults.Bind(nil, nil)

	// Record's contract is that it NEVER fails: it is called from a live TUI's
	// render tick, where a diagnostic failure must not become a user-visible
	// one. An unbound package must therefore drop the fault, not panic.
	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobFailed,
		Source:  "job:orphan",
		Summary: "recorded with no binding at all",
	})

	if faults.Bound() {
		t.Error("Bound() is true after binding nil")
	}
}

func TestClear_TargetsSpecificIncidents(t *testing.T) {
	_, _ = newBoundStore(t)

	faults.Record(faults.Fault{Code: faults.ErrCodeJobFailed, Source: "job:a", Summary: "job a failed"})
	faults.Record(faults.Fault{Code: faults.ErrCodeJobFailed, Source: "job:b", Summary: "job b failed"})

	incidents, err := faults.List(false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(incidents) != 2 {
		t.Fatalf("%d open incidents, want 2", len(incidents))
	}

	cleared, err := faults.Clear([]int64{incidents[0].ID}, "tester")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared != 1 {
		t.Errorf("cleared %d, want 1", cleared)
	}

	remaining, err := faults.List(false, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != 1 {
		t.Errorf("%d open incidents remain, want 1", len(remaining))
	}
}

func TestRecord_WritesNothingBeyondTheTableAndItsOwnLog(t *testing.T) {
	_, logDir := newBoundStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobFailed,
		Source:  "job:evaluate",
		Summary: "job \"evaluate\" failed",
		Detail:  "detail that must not reach the ledger",
	})

	// Faults are MACHINE-LOCAL observation, not shareable project history. The
	// db-ledger is an append-only DIRECTORY of segment files (see
	// internal/events/writer.go, LedgerDirName) that IS committed to the project
	// repo, so a fault landing there would turn one developer's transient job
	// failure into everyone's permanent history.
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("read log dir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "errors.jsonl" {
			t.Errorf("recording a fault created %q; only errors.jsonl is expected", entry.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("log dir has %d entries, want exactly 1 (errors.jsonl)", len(entries))
	}

	if _, err = os.Stat(filepath.Join(logDir, "db-ledger")); !os.IsNotExist(err) {
		t.Error("recording a fault created a db-ledger directory; faults must never reach the ledger")
	}
}

func TestCatalog_EveryCodeIsDocumented(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "errors.md"))
	if err != nil {
		t.Fatalf("read docs/errors.md: %v", err)
	}
	docs := string(data)

	codes := faults.Codes()
	if len(codes) == 0 {
		t.Fatal("catalog is empty")
	}

	// Every catalog entry must have a section, so a code cannot ship
	// undocumented and leave a user with a number and no explanation.
	for _, code := range codes {
		heading := "## " + code.ID + " — " + code.Slug
		if !strings.Contains(docs, heading) {
			t.Errorf("docs/errors.md has no section %q", heading)
		}
	}

	// ...and every documented section must map to a real code, so the docs
	// cannot go stale describing a code that no longer exists.
	documented := regexp.MustCompile(`(?m)^## (ERR-\d{4}) — ([a-z0-9-]+)$`).FindAllStringSubmatch(docs, -1)
	if len(documented) != len(codes) {
		t.Errorf("docs document %d codes, catalog has %d", len(documented), len(codes))
	}
	for _, match := range documented {
		code, ok := faults.LookupCode(match[1])
		if !ok {
			t.Errorf("docs/errors.md documents %s, which is not in the catalog", match[1])
			continue
		}
		if code.Slug != match[2] {
			t.Errorf("docs/errors.md calls %s %q, catalog calls it %q", match[1], match[2], code.Slug)
		}
	}
}

func TestCatalog_CodesAreUniqueAndWellFormed(t *testing.T) {
	seen := make(map[string]bool)
	format := regexp.MustCompile(`^ERR-\d{4}$`)

	for _, code := range faults.Codes() {
		if seen[code.ID] {
			t.Errorf("duplicate code id %s", code.ID)
		}
		seen[code.ID] = true

		// Codes must NOT look like task ids (E-NNNN, or a future per-project
		// prefix): a number that could be either is ambiguous in every log line.
		if !format.MatchString(code.ID) {
			t.Errorf("code id %q is not of the form ERR-NNNN", code.ID)
		}
		if code.Severity != faults.SeverityWarning && code.Severity != faults.SeverityError {
			t.Errorf("%s has severity %q, want warning or error", code.ID, code.Severity)
		}
		if code.Title == "" {
			t.Errorf("%s has no title", code.ID)
		}
	}
}
