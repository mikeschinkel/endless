package outagejob

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/outages"
	"github.com/mikeschinkel/endless/internal/schema"
)

func newTestDB(t *testing.T) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err = schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	outages.Bind(func() (*sql.DB, error) { return db, nil })
	t.Cleanup(func() { outages.Bind(nil) })
}

func statusPage(t *testing.T, status *string) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	t.Cleanup(outages.SetFetchForTest(func(context.Context, string) ([]byte, error) {
		calls.Add(1)
		return []byte(`{"components":[{"name":"Git Operations","status":"` + *status + `"}]}`), nil
	}))
	return &calls
}

// P18: no outage open, no request.
func TestCheck_NoneOpenMakesNoRequest(t *testing.T) {
	newTestDB(t)
	status := "major_outage"
	calls := statusPage(t, &status)

	note, err := check(context.Background())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("made %d status-page requests with no outage open", calls.Load())
	}
	if note != "no outage open" {
		t.Errorf("note = %q", note)
	}
}

// P18, P19: an open outage is polled; operational closes it and runs the
// close hooks (main-sync's makes it due at once).
func TestCheck_PollsAndClosesWhenOperational(t *testing.T) {
	newTestDB(t)
	status := "partial_outage"
	calls := statusPage(t, &status)
	var reopened int
	outages.OnClose("GitHub", func() { reopened++ })

	if err := outages.Open(outages.GitHub, "degraded_performance", "test"); err != nil {
		t.Fatalf("open: %v", err)
	}

	if _, err := check(context.Background()); err != nil {
		t.Fatalf("check: %v", err)
	}
	open, _ := outages.List(false)
	if len(open) != 1 || open[0].Status != "partial_outage" {
		t.Fatalf("after a degraded read: %+v, want it open at partial_outage", open)
	}

	status = outages.Operational
	note, err := check(context.Background())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if open, _ = outages.List(false); len(open) != 0 {
		t.Fatalf("after an operational read: %+v, want closed", open)
	}
	if calls.Load() != 2 {
		t.Errorf("requests = %d, want 2", calls.Load())
	}
	if reopened < 1 {
		t.Error("closing the GitHub outage ran no close hook")
	}
	if note != "GitHub: operational, closed" {
		t.Errorf("note = %q", note)
	}
}
