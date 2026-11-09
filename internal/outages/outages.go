// Package outages records provider outages that the provider's own status page
// confirmed, and shows them as notices that clear themselves (E-2589).
//
// # Why this is not a fault
//
// A fault asks a person to do something: read it, fix the cause, clear it.
// When GitHub's Git Operations are degraded and a fetch fails, or Anthropic's
// API is degraded and a turn dies, there is nothing for the person to fix and
// nothing they would learn by debugging their own machine. Raising a fault for
// it fills the fault row with something to clear that clears itself.
//
// So an outage is its own record. It opens when a failure that depends on a
// provider coincides with that provider's status page reporting the component
// degraded; the outage-check job (internal/outagejob) polls the status page
// once a minute while one is open; and it closes when the component reads
// operational again. It carries no code, no severity and no action hint, and
// nobody clears it.
//
// # When the status page says nothing
//
// A provider posts an incident late or not at all — GitHub posted nothing for
// one of the two windows that motivated this package. So a status check never
// stands in for a source's own handling: when the request fails, or the
// component reads operational, Confirm says no and the caller handles the
// failure exactly as it did before (a transient run, or a fault).
//
// # Providers
//
// A provider is an Atlassian Statuspage summary URL plus the one component a
// failure depends on. GitHub and Anthropic both use Statuspage, and are the
// only two (P11 of E-2589).
package outages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Provider is one status page and the component on it that a failure depends
// on.
type Provider struct {
	Name       string // shown to people: "GitHub"
	SummaryURL string // the page's api/v2/summary.json
	Component  string // the component's name on that page, exactly
}

var (
	// GitHub is watched for Git Operations: what a fetch or push to a
	// github.com remote depends on.
	GitHub = Provider{
		Name:       "GitHub",
		SummaryURL: "https://www.githubstatus.com/api/v2/summary.json",
		Component:  "Git Operations",
	}

	// Anthropic is watched for the Claude API: what a Claude Code turn depends
	// on. status.anthropic.com redirects to status.claude.com, so the request
	// goes there directly.
	Anthropic = Provider{
		Name:       "Anthropic",
		SummaryURL: "https://status.claude.com/api/v2/summary.json",
		Component:  "Claude API (api.anthropic.com)",
	}
)

// Providers returns every provider, in display order.
func Providers() []Provider {
	return []Provider{GitHub, Anthropic}
}

// Lookup returns the provider with the given name.
func Lookup(name string) (p Provider, ok bool) {
	for _, candidate := range Providers() {
		if candidate.Name == name {
			return candidate, true
		}
	}
	return Provider{}, false
}

// PollInterval is how often the outage-check job reads the status page of each
// provider with an open outage.
const PollInterval = time.Minute

// RequestTimeout bounds one status-page request. Callers include a hook and a
// background job; neither may hang on a status page that is itself down.
const RequestTimeout = 4 * time.Second

// Operational is the Statuspage status of a component with no incident.
const Operational = "operational"

// fetch reads a URL. A var so tests answer without a network.
var fetch = func(ctx context.Context, url string) (body []byte, err error) {
	var req *http.Request
	var resp *http.Response

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// SetFetchForTest replaces the status-page request and returns a restore
// func. Tests only.
func SetFetchForTest(fn func(ctx context.Context, url string) ([]byte, error)) (restore func()) {
	prev := fetch
	fetch = fn
	return func() { fetch = prev }
}

// summary is the part of a Statuspage summary.json this package reads.
type summary struct {
	Components []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"components"`
}

// Read returns the provider's component status as its status page reports it
// now: "operational", "degraded_performance", "partial_outage",
// "major_outage" or "under_maintenance". An error means the page could not be
// read or does not list the component, which is not an answer either way.
func Read(ctx context.Context, p Provider) (status string, err error) {
	var body []byte
	var s summary

	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()

	body, err = fetch(ctx, p.SummaryURL)
	if err != nil {
		return "", fmt.Errorf("read %s status: %w", p.Name, err)
	}
	err = json.Unmarshal(body, &s)
	if err != nil {
		return "", fmt.Errorf("parse %s status: %w", p.Name, err)
	}
	for _, c := range s.Components {
		if c.Name == p.Component {
			return c.Status, nil
		}
	}
	return "", fmt.Errorf("%s status page lists no component %q", p.Name, p.Component)
}

// Confirm reports whether p has an outage on its component now, and opens the
// outage record when it does. openedBy names the failure that asked, for the
// record.
//
// An outage already open answers yes without a request: the outage-check job
// keeps it current, and a burst of failures during one outage must not become a
// burst of requests to a status page. Any failure to read the page answers no —
// see the package doc.
func Confirm(ctx context.Context, p Provider, openedBy string) (confirmed bool) {
	var status string
	var open bool
	var err error

	open, err = IsOpen(p)
	if err == nil && open {
		confirmed = true
		goto end
	}
	status, err = Read(ctx, p)
	if err != nil || status == Operational || status == "" {
		goto end
	}
	err = Open(p, status, openedBy)
	confirmed = err == nil

end:
	return confirmed
}

// Outage is one row of the outages table.
type Outage struct {
	ID        int64
	Provider  string
	Component string
	Status    string
	OpenedBy  string
	OpenedAt  time.Time
	CheckedAt time.Time
	ClosedAt  time.Time // zero while open
}

// Open reports whether the outage is still open.
func (o Outage) Open() bool { return o.ClosedAt.IsZero() }

// Lasted is how long the outage has been open, or lasted once closed.
func (o Outage) Lasted(now time.Time) time.Duration {
	end := now
	if !o.Open() {
		end = o.ClosedAt
	}
	return end.Sub(o.OpenedAt)
}

// ErrNotBound is every store call's answer before Bind.
var ErrNotBound = errors.New("outages: no database bound")

var (
	bindMu     sync.RWMutex
	databaseFn func() (*sql.DB, error)
)

// Bind wires the database accessor, once at process start
// (cmd/endless-go/main.go, beside faults.Bind) and by tests. Until it is
// called every store call returns ErrNotBound, so a process that never wired
// one — a test of some other package — reads no database at all rather than
// whichever one its environment happens to resolve, and the status views show
// no outage line.
func Bind(db func() (*sql.DB, error)) {
	bindMu.Lock()
	databaseFn = db
	bindMu.Unlock()
}

func database() (*sql.DB, error) {
	bindMu.RLock()
	fn := databaseFn
	bindMu.RUnlock()
	if fn == nil {
		return nil, ErrNotBound
	}
	return fn()
}

const sqlNow = `strftime('%Y-%m-%dT%H:%M:%S', 'now')`

// Open opens p's outage with the component status read, or refreshes the one
// already open. One open outage per provider: idx_outages_open turns a second
// opener into the DO UPDATE branch.
func Open(p Provider, status, openedBy string) (err error) {
	var db *sql.DB

	db, err = database()
	if err != nil {
		return fmt.Errorf("open %s outage: %w", p.Name, err)
	}
	_, err = db.Exec(
		`INSERT INTO outages (provider, component, status, opened_by, opened_at, checked_at)
		 VALUES (?, ?, ?, ?, `+sqlNow+`, `+sqlNow+`)
		 ON CONFLICT (provider) WHERE closed_at IS NULL
		 DO UPDATE SET status = excluded.status, checked_at = excluded.checked_at`,
		p.Name, p.Component, status, openedBy,
	)
	if err != nil {
		return fmt.Errorf("open %s outage: %w", p.Name, err)
	}
	return nil
}

// IsOpen reports whether p has an open outage.
func IsOpen(p Provider) (open bool, err error) {
	var db *sql.DB

	db, err = database()
	if err != nil {
		return false, err
	}
	err = db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM outages WHERE provider = ? AND closed_at IS NULL)`,
		p.Name,
	).Scan(&open)
	return open, err
}

// Refresh records the status read for p's open outage and when it was read.
func Refresh(p Provider, status string) (err error) {
	var db *sql.DB

	db, err = database()
	if err != nil {
		return err
	}
	_, err = db.Exec(
		`UPDATE outages SET status = ?, checked_at = `+sqlNow+`
		  WHERE provider = ? AND closed_at IS NULL`,
		status, p.Name,
	)
	return err
}

// Close closes p's open outage and reports whether there was one. The
// provider's close hooks (OnClose) run after a close, in this process.
func Close(p Provider) (closed bool, err error) {
	var db *sql.DB
	var result sql.Result
	var affected int64

	db, err = database()
	if err != nil {
		return false, err
	}
	result, err = db.Exec(
		`UPDATE outages SET status = ?, checked_at = `+sqlNow+`, closed_at = `+sqlNow+`
		  WHERE provider = ? AND closed_at IS NULL`,
		Operational, p.Name,
	)
	if err != nil {
		return false, err
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return false, err
	}
	closed = affected > 0
	if closed {
		runCloseHooks(p.Name)
	}
	return closed, nil
}

// List returns the open outages, oldest first, and the closed ones newest
// first after them when includeClosed is set.
func List(includeClosed bool) (outages []Outage, err error) {
	var db *sql.DB
	var rows *sql.Rows
	var where string

	db, err = database()
	if err != nil {
		return nil, err
	}
	where = `WHERE closed_at IS NULL`
	if includeClosed {
		where = ``
	}
	rows, err = db.Query(
		`SELECT id, provider, component, status, opened_by, opened_at, checked_at,
		        COALESCE(closed_at, '')
		   FROM outages ` + where + `
		  ORDER BY closed_at IS NOT NULL, CASE WHEN closed_at IS NULL THEN opened_at END,
		           closed_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var o Outage
		var opened, checked, closed string
		err = rows.Scan(&o.ID, &o.Provider, &o.Component, &o.Status, &o.OpenedBy,
			&opened, &checked, &closed)
		if err != nil {
			return nil, err
		}
		o.OpenedAt, o.CheckedAt, o.ClosedAt = parseStamp(opened), parseStamp(checked), parseStamp(closed)
		outages = append(outages, o)
	}
	return outages, rows.Err()
}

// parseStamp reads a schema timestamp as UTC; "" and garbage are the zero time.
func parseStamp(s string) (t time.Time) {
	t, _ = time.Parse("2006-01-02T15:04:05", s)
	return t
}

var (
	hooksMu    sync.Mutex
	closeHooks = map[string][]func(){}
)

// OnClose registers fn to run when the named provider's outage closes in this
// process. main-sync registers one for GitHub so a closed outage makes it due
// at once instead of waiting out its backoff. Call it from init().
func OnClose(provider string, fn func()) {
	hooksMu.Lock()
	closeHooks[provider] = append(closeHooks[provider], fn)
	hooksMu.Unlock()
}

func runCloseHooks(provider string) {
	hooksMu.Lock()
	hooks := append([]func(){}, closeHooks[provider]...)
	hooksMu.Unlock()
	for _, fn := range hooks {
		fn()
	}
}

// StatusText renders a Statuspage component status for a person.
func StatusText(status string) string {
	switch status {
	case "degraded_performance":
		return "Degraded"
	case "partial_outage":
		return "Partial outage"
	case "major_outage":
		return "Major outage"
	case "under_maintenance":
		return "Under maintenance"
	case Operational:
		return "Operational"
	}
	s := strings.ReplaceAll(status, "_", " ")
	if s == "" {
		return "Degraded"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Duration renders d in at most two units, largest first, so a reader need
// not do clock arithmetic: "2 days 3 hrs", "4 hrs 59 min", "9 min 49 sec",
// "49 sec". A zero second unit is left off: "1 min", "2 hrs".
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int64(d / time.Second)
	days, hrs, mins := secs/86400, secs/3600%24, secs/60%60
	secs %= 60
	switch {
	case days > 0:
		return joinUnits(days, "day", "days", hrs, "hr", "hrs")
	case hrs > 0:
		return joinUnits(hrs, "hr", "hrs", mins, "min", "min")
	case mins > 0:
		return joinUnits(mins, "min", "min", secs, "sec", "sec")
	}
	return unit(secs, "sec", "sec")
}

func joinUnits(a int64, aOne, aMany string, b int64, bOne, bMany string) string {
	s := unit(a, aOne, aMany)
	if b > 0 {
		s += " " + unit(b, bOne, bMany)
	}
	return s
}

func unit(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.FormatInt(n, 10) + " " + many
}
