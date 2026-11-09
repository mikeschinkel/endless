package outages

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

func newTestDB(t *testing.T) *sql.DB {
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
	Bind(func() (*sql.DB, error) { return db, nil })
	t.Cleanup(func() { Bind(nil) })
	return db
}

// statusPage answers every request with a summary listing component at status,
// and counts the requests.
func statusPage(t *testing.T, component, status string) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	t.Cleanup(SetFetchForTest(func(ctx context.Context, url string) ([]byte, error) {
		calls.Add(1)
		return []byte(`{"components":[{"name":"Webhooks","status":"major_outage"},` +
			`{"name":"` + component + `","status":"` + status + `"}]}`), nil
	}))
	return &calls
}

func TestDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                              "0 sec",
		49 * time.Second:               "49 sec",
		time.Minute:                    "1 min",
		9*time.Minute + 49*time.Second: "9 min 49 sec",
		time.Hour + 3*time.Minute:      "1 hr 3 min",
		4*time.Hour + 59*time.Minute:   "4 hrs 59 min",
		2 * time.Hour:                  "2 hrs",
		27*time.Hour + 10*time.Minute:  "1 day 3 hrs",
		50*time.Hour + 59*time.Second:  "2 days 2 hrs",
		-5 * time.Second:               "0 sec",
	}
	for d, want := range cases {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestRead_FindsTheComponent(t *testing.T) {
	statusPage(t, GitHub.Component, "partial_outage")
	got, err := Read(context.Background(), GitHub)
	if err != nil || got != "partial_outage" {
		t.Fatalf("Read = %q, %v; want partial_outage", got, err)
	}
}

func TestRead_MissingComponentIsAnError(t *testing.T) {
	statusPage(t, "Something Else", "major_outage")
	if _, err := Read(context.Background(), GitHub); err == nil {
		t.Fatal("a page without the component read as an answer")
	}
}

// P3: a degraded component opens an outage, and a second failure during it
// asks no second request.
func TestConfirm_DegradedOpensOnceAndStopsAsking(t *testing.T) {
	newTestDB(t)
	calls := statusPage(t, GitHub.Component, "degraded_performance")

	if !Confirm(context.Background(), GitHub, "main-sync: fetch origin") {
		t.Fatal("a degraded component was not confirmed")
	}
	if !Confirm(context.Background(), GitHub, "main-sync: fetch origin") {
		t.Fatal("the open outage was not confirmed")
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("status page requested %d times, want 1", n)
	}
	open, err := List(false)
	if err != nil || len(open) != 1 {
		t.Fatalf("List = %+v, %v; want one open outage", open, err)
	}
	o := open[0]
	if o.Provider != "GitHub" || o.Component != "Git Operations" ||
		o.Status != "degraded_performance" || o.OpenedBy != "main-sync: fetch origin" {
		t.Errorf("outage = %+v", o)
	}
}

// P5: an operational component, or a status page that cannot be read, is no
// confirmation and opens nothing.
func TestConfirm_NoIncidentOrNoAnswerIsNo(t *testing.T) {
	newTestDB(t)

	statusPage(t, GitHub.Component, Operational)
	if Confirm(context.Background(), GitHub, "x") {
		t.Error("an operational component was confirmed")
	}

	t.Cleanup(SetFetchForTest(func(context.Context, string) ([]byte, error) {
		return nil, errors.New("dial tcp: no route to host")
	}))
	if Confirm(context.Background(), GitHub, "x") {
		t.Error("an unreadable status page was confirmed")
	}

	if open, _ := List(true); len(open) != 0 {
		t.Errorf("outages = %+v, want none", open)
	}
}

func TestClose_RunsHooksAndKeepsHistory(t *testing.T) {
	newTestDB(t)
	statusPage(t, Anthropic.Component, "major_outage")
	var ran int
	OnClose("Anthropic", func() { ran++ })
	t.Cleanup(func() { delete(closeHooks, "Anthropic") })

	Confirm(context.Background(), Anthropic, "hook:stopfailure")
	closed, err := Close(Anthropic)
	if err != nil || !closed {
		t.Fatalf("Close = %v, %v", closed, err)
	}
	if ran != 1 {
		t.Errorf("close hook ran %d times, want 1", ran)
	}
	if closed, _ = Close(Anthropic); closed {
		t.Error("closed an outage twice")
	}
	if open, _ := List(false); len(open) != 0 {
		t.Errorf("open = %+v, want none", open)
	}
	all, _ := List(true)
	if len(all) != 1 || all[0].Open() || all[0].Status != Operational {
		t.Errorf("history = %+v, want one closed outage", all)
	}
}

func TestLine(t *testing.T) {
	now := time.Date(2026, 11, 4, 14, 49, 0, 0, time.UTC)
	o := Outage{Provider: "GitHub", Status: "degraded_performance",
		OpenedAt: now.Add(-(4*time.Hour + 59*time.Minute))}

	want := "Outage[GitHub]: Degraded for 4 hrs 59 min; polling every 1 min"
	if got := Line(o, now, 120, false); got != want {
		t.Errorf("Line = %q, want %q", got, want)
	}

	colored := Line(o, now, 120, true)
	plain := regexpStrip(colored)
	if plain != want {
		t.Errorf("coloured line reads %q, want %q", plain, want)
	}
	if strings.Contains(colored, "\033[7m") || strings.Contains(colored, "48;5;") {
		t.Error("the outage line is reversed or has a background: it must read calm, not as a fault bar")
	}
	if strings.Contains(colored, "eeh") {
		t.Error("the outage line carries an action hint")
	}

	for cols := 1; cols < 80; cols++ {
		got := Line(o, now, cols, false)
		if w := len([]rune(got)); got != "" && w > cols-1 {
			t.Fatalf("at %d cols the line is %d wide: %q", cols, w, got)
		}
	}
}

func TestAgentContext(t *testing.T) {
	now := time.Now()
	got := AgentContext([]Outage{{Provider: "GitHub", Component: "Git Operations",
		Status: "partial_outage", OpenedAt: now.Add(-time.Hour)}}, now)
	for _, want := range []string{"GitHub", "Git Operations", "partial outage", "not a local problem"} {
		if !strings.Contains(got, want) {
			t.Errorf("context %q lacks %q", got, want)
		}
	}
	if AgentContext(nil, now) != "" {
		t.Error("no outages produced context")
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func regexpStrip(s string) string { return ansi.ReplaceAllString(s, "") }

func TestUnbound_ReadsNothing(t *testing.T) {
	Bind(nil)
	if _, err := List(false); !errors.Is(err, ErrNotBound) {
		t.Errorf("List unbound: err = %v, want ErrNotBound", err)
	}
}
