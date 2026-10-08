package errorscmd

import (
	"database/sql"
	"io"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/schema"
)

// E-2268: who raised each fault, on `errors list` and `errors show`.

func TestRaiserText(t *testing.T) {
	tests := []struct {
		task, session int64
		want          string
	}{
		{2259, 1299, "ES-1299 (E-2259)"},
		{0, 1299, "ES-1299"},
		{2259, 0, "E-2259"},
		{0, 0, "-"},
	}
	for _, tt := range tests {
		if got := raiserText(tt.task, tt.session); got != tt.want {
			t.Errorf("raiserText(%d, %d) = %q, want %q", tt.task, tt.session, got, tt.want)
		}
	}
}

func TestByText_MarksTheOtherRaisers(t *testing.T) {
	tests := []struct {
		raisers int64
		want    string
	}{
		{0, "ES-1299 (E-2259)"}, // recorded before raisers were tracked
		{1, "ES-1299 (E-2259)"},
		{3, "ES-1299 (E-2259) +2"},
	}
	for _, tt := range tests {
		i := faults.Incident{TaskID: 2259, SessionID: 1299, Raisers: tt.raisers}
		if got := byText(i); got != tt.want {
			t.Errorf("byText(raisers=%d) = %q, want %q", tt.raisers, got, tt.want)
		}
	}
}

func TestListingLines_CarryTheBYColumn(t *testing.T) {
	lines := listingLines([]faults.Incident{warned(), errored()}, false, false, 0)
	if !strings.Contains(lines[0], "BY") {
		t.Fatalf("heading %q has no BY column", lines[0])
	}
	if !strings.Contains(lines[1], "ES-1299 (E-2259) +2") {
		t.Errorf("row %q does not name its latest raiser and the two others", lines[1])
	}
	if offsetOf(lines[2], " - ") < 0 {
		t.Errorf("row %q does not mark an unattributed raiser with -", lines[2])
	}
}

// captureStdout runs fn and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = saved
	if err = w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out)
}

func TestShow_ListsEveryRaiserAndEachOccurrencesOwn(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err = schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	logDir := t.TempDir()
	bind := func(r faults.Raiser) {
		faults.Bind(
			func() (*sql.DB, error) { return db, nil },
			func() string { return logDir },
			nil,
			func(faults.Raiser) faults.Raiser { return r },
		)
	}
	t.Cleanup(func() { faults.Bind(nil, nil, nil, nil) })

	f := faults.Fault{Code: faults.ErrCodeJobFailed, Source: "job:x", Summary: "job \"x\" failed"}
	bind(faults.Raiser{TaskID: 2259, SessionID: 1299})
	faults.Record(f)
	faults.Record(f)
	bind(faults.Raiser{TaskID: 2268})
	faults.Record(f)

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil || len(incidents) != 1 {
		t.Fatalf("list = %d incidents, err %v; want 1", len(incidents), err)
	}

	out := captureStdout(t, func() { showOne(incidents[0].ID, true) })

	for _, want := range []string{
		"Raised by:",
		"E-2268            1 occurrence(s)",
		"ES-1299 (E-2259)  2 occurrence(s)",
		"occurrence 1, raised by ES-1299 (E-2259)",
		"occurrence 3, raised by E-2268",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show output lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "E-2268            1") > strings.Index(out, "ES-1299 (E-2259)  2") {
		t.Errorf("raisers are not most-recent first:\n%s", out)
	}
}
