package triagejob

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/schema"
)

// fakeFX records what routing did and answers what it asks from fields.
type fakeFX struct {
	sessions    map[int64]sessionInfo
	transcripts map[string]bool
	delivery    delivery
	deliverErr  error
	throttled   string // non-empty: startAllowed says no, with this reason
	settled     map[int64]bool
	claimed     map[int64]bool
	clock       time.Time

	delivered []string // pane per delivery
	messages  []string
	resumed   []int64
	filed     []taskSpec
	contexts  map[int64]string
	spawned   []int64
	nextTask  int64
}

func newFake() *fakeFX {
	return &fakeFX{
		sessions: map[int64]sessionInfo{}, transcripts: map[string]bool{},
		delivery: delivered, settled: map[int64]bool{}, claimed: map[int64]bool{},
		contexts: map[int64]string{}, nextTask: 900,
		clock: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC),
	}
}

func (f *fakeFX) session(id int64) (sessionInfo, error) { return f.sessions[id], nil }
func (f *fakeFX) transcriptExists(uuid string) bool     { return f.transcripts[uuid] }
func (f *fakeFX) deliver(_ context.Context, pane, message string) (delivery, error) {
	f.delivered = append(f.delivered, pane)
	f.messages = append(f.messages, message)
	return f.delivery, f.deliverErr
}
func (f *fakeFX) startAllowed() (bool, string, error) { return f.throttled == "", f.throttled, nil }
func (f *fakeFX) resume(_ context.Context, id int64, _ string, _ string) error {
	f.resumed = append(f.resumed, id)
	return nil
}
func (f *fakeFX) fileTask(_ context.Context, _ string, spec taskSpec) (int64, error) {
	f.nextTask++
	f.filed = append(f.filed, spec)
	return f.nextTask, nil
}
func (f *fakeFX) updateContext(_ context.Context, _ string, id int64, text string) error {
	f.contexts[id] = text
	return nil
}
func (f *fakeFX) spawn(_ context.Context, _ string, id int64) (string, error) {
	f.spawned = append(f.spawned, id)
	f.claimed[id] = true
	return "", nil
}
func (f *fakeFX) fixTask(id int64) (bool, bool, error) { return f.settled[id], f.claimed[id], nil }
func (f *fakeFX) now() time.Time                       { return f.clock }

// harness binds an in-memory fault store and a router over fx.
func harness(t *testing.T) (*router, *fakeFX, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err = schema.Migrate(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	logDir := t.TempDir()
	faults.Bind(func() (*sql.DB, error) { return db, nil }, func() string { return logDir }, nil, nil)
	t.Cleanup(func() { faults.Bind(nil, nil, nil, nil) })

	fx := newFake()
	return &router{fx: fx, paths: map[int64]string{0: "/projects/p"}}, fx, db
}

// raise records one fault raised by (task, session) and returns its incident.
func raise(t *testing.T, fingerprint string, taskID, sessionID int64) faults.Incident {
	t.Helper()
	faults.Record(faults.Fault{
		Code: faults.ErrCodeJobFailed, Source: "job:test", Fingerprint: fingerprint,
		Summary: "job \"test\" failed: " + fingerprint, TaskID: taskID, SessionID: sessionID,
	})
	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range incidents {
		if i.Fingerprint == fingerprint {
			return i
		}
	}
	t.Fatalf("incident %q not recorded", fingerprint)
	return faults.Incident{}
}

// step routes the incident once from its stored triage row.
func step(t *testing.T, r *router, id int64, escalated bool) faults.Triage {
	t.Helper()
	in, ok, err := faults.Get(id)
	if err != nil || !ok {
		t.Fatalf("get %d: %v", id, err)
	}
	tr, has, err := faults.GetTriage(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.route(context.Background(), faults.Routable{Incident: in, Triage: tr, HasRow: has}, escalated); err != nil {
		t.Fatalf("route %d: %v", id, err)
	}
	tr, _, err = faults.GetTriage(id)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func live(idle bool) sessionInfo {
	return sessionInfo{found: true, live: true, idle: idle, pane: "%373", uuid: "u-1", taskID: 50,
		lastActivity: "2026-10-08T11:00:00"}
}

func TestRoute_IdleLiveSessionIsMessagedByPane(t *testing.T) {
	r, fx, _ := harness(t)
	fx.sessions[7] = live(true)
	in := raise(t, "a", 50, 7)

	tr := step(t, r, in.ID, false)
	if tr.State != faults.TriageDelivered || tr.SessionID != 7 || tr.DeliveredAt == "" {
		t.Fatalf("triage = %+v, want delivered to ES-7", tr)
	}
	if len(fx.delivered) != 1 || fx.delivered[0] != "%373" {
		t.Fatalf("delivered to %v, want pane %%373", fx.delivered)
	}
	msg := fx.messages[0]
	for _, want := range []string{"we think error", "endless errors accept", "endless errors decline"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
}

func TestRoute_BusySessionWaitsUntilIdleUnlessEscalated(t *testing.T) {
	r, fx, _ := harness(t)
	fx.sessions[7] = live(false)
	in := raise(t, "a", 50, 7)

	if tr := step(t, r, in.ID, false); tr.State != faults.TriageWaiting || len(fx.delivered) != 0 {
		t.Fatalf("busy: triage %+v, %d deliveries; want waiting, none", tr, len(fx.delivered))
	}
	if tr := step(t, r, in.ID, true); tr.State != faults.TriageDelivered {
		t.Fatalf("escalated: state %s, want delivered", tr.State)
	}

	// A stored escalation is honoured by the job's own next run too.
	in2 := raise(t, "b", 50, 7)
	if err := faults.Escalate(in2.ID); err != nil {
		t.Fatal(err)
	}
	if tr := step(t, r, in2.ID, false); tr.State != faults.TriageDelivered {
		t.Fatalf("stored escalation: state %s, want delivered", tr.State)
	}
}

func TestRoute_HeldMessageFallsBackToFileAndSpawn(t *testing.T) {
	r, fx, _ := harness(t)
	fx.sessions[7] = live(true)
	fx.delivery = held
	in := raise(t, "a", 50, 7)

	tr := step(t, r, in.ID, false)
	if tr.State != faults.TriageSpawned || tr.FixTaskID != 901 {
		t.Fatalf("triage = %+v, want spawned E-901", tr)
	}
	if len(fx.filed) != 1 || len(fx.spawned) != 1 {
		t.Fatalf("filed %d, spawned %d; want 1 each", len(fx.filed), len(fx.spawned))
	}
	spec := fx.filed[0]
	if len(spec.cleansUp) != 1 || spec.cleansUp[0] != 50 {
		t.Errorf("cleansUp = %v, want the raising task E-50", spec.cleansUp)
	}
	for _, want := range []string{"## Error", "not delivered (HELD)", "ED-1614", "errors accept"} {
		if !strings.Contains(spec.context, want) {
			t.Errorf("context lacks %q:\n%s", want, spec.context)
		}
	}
	if spec.plan == "" {
		t.Error("filed with no plan: spawn would refuse it")
	}
}

func TestRoute_SenderFailureFallsBackAndFailsTheRun(t *testing.T) {
	r, fx, _ := harness(t)
	fx.sessions[7] = live(true)
	fx.deliverErr = errors.New("claude not found")
	in := raise(t, "a", 50, 7)

	tr, _, _ := faults.GetTriage(in.ID)
	_, err := r.route(context.Background(), faults.Routable{Incident: in, Triage: tr}, false)
	if err == nil {
		t.Fatal("a broken sender must fail the run, so it is seen")
	}
	if got, _, _ := faults.GetTriage(in.ID); got.State != faults.TriageQueued {
		t.Fatalf("state %s, want queued for file-and-spawn", got.State)
	}
}

func TestRoute_EndedSessionWithTranscriptIsResumedAndThrottled(t *testing.T) {
	r, fx, _ := harness(t)
	fx.sessions[7] = sessionInfo{found: true, uuid: "u-7", taskID: 50}
	fx.transcripts["u-7"] = true
	fx.throttled = "fix task E-1 is still being worked"
	in := raise(t, "a", 50, 7)

	if tr := step(t, r, in.ID, false); tr.State != faults.TriageWaiting || len(fx.resumed) != 0 {
		t.Fatalf("throttled: %+v, resumed %v; want waiting, none", tr, fx.resumed)
	}
	fx.throttled = ""
	if tr := step(t, r, in.ID, false); tr.State != faults.TriageResumed || len(fx.resumed) != 1 {
		t.Fatalf("open: %+v, resumed %v; want resumed ES-7", tr, fx.resumed)
	}
}

func TestRoute_EndedSessionWithoutTranscriptIsFiled(t *testing.T) {
	r, fx, _ := harness(t)
	fx.sessions[7] = sessionInfo{found: true, uuid: "u-7", taskID: 50}
	in := raise(t, "a", 50, 7)

	if tr := step(t, r, in.ID, false); tr.State != faults.TriageSpawned {
		t.Fatalf("state %s, want spawned", tr.State)
	}
}

func TestRoute_ThrottleHoldsASpawnUntilOpen(t *testing.T) {
	r, fx, _ := harness(t)
	fx.throttled = "busy"
	in := raise(t, "a", 0, 0)

	if tr := step(t, r, in.ID, false); tr.State != faults.TriageFiled || len(fx.spawned) != 0 {
		t.Fatalf("throttled: %+v; want filed, not spawned", tr)
	}
	fx.throttled = ""
	if tr := step(t, r, in.ID, false); tr.State != faults.TriageSpawned || len(fx.filed) != 1 {
		t.Fatalf("open: %+v, filed %d; want spawned and filed once", tr, len(fx.filed))
	}
}

func TestRoute_DeclineFilesAndSpawns(t *testing.T) {
	r, fx, _ := harness(t)
	fx.sessions[7] = live(true)
	in := raise(t, "a", 50, 7)
	step(t, r, in.ID, false)

	if err := faults.Decline(in.ID, 7, "not mine: the rater raised it"); err != nil {
		t.Fatal(err)
	}
	if tr := step(t, r, in.ID, false); tr.State != faults.TriageSpawned {
		t.Fatalf("state %s, want spawned", tr.State)
	}
	if !strings.Contains(fx.filed[0].context, "ES-7 declined it: not mine") {
		t.Errorf("context lacks the decline:\n%s", fx.filed[0].context)
	}
}

func TestRoute_AcceptSettlesIt(t *testing.T) {
	r, fx, _ := harness(t)
	fx.sessions[7] = live(true)
	in := raise(t, "a", 50, 7)
	step(t, r, in.ID, false)

	if err := faults.Accept(in.ID, 7); err != nil {
		t.Fatal(err)
	}
	routables, err := faults.Routables([]int64{0})
	if err != nil {
		t.Fatal(err)
	}
	if len(routables) != 0 {
		t.Fatalf("an accepted incident is still routable: %+v", routables)
	}
	got, _, _ := faults.Get(in.ID)
	if got.AcceptedSessionID != 7 {
		t.Fatalf("AcceptedSessionID = %d, want 7", got.AcceptedSessionID)
	}
}

func TestRoute_IdleWithoutAnswerFallsBackAfterTheGrace(t *testing.T) {
	r, fx, _ := harness(t)
	s := live(true)
	fx.sessions[7] = s
	in := raise(t, "a", 50, 7)
	tr := step(t, r, in.ID, false)

	// It took its turn and went idle again, inside the grace: no verdict yet.
	s.lastActivity = "2026-10-08T12:01:00"
	fx.sessions[7] = s
	fx.clock = fx.clock.Add(2 * time.Minute)
	if tr = step(t, r, in.ID, false); tr.State != faults.TriageDelivered {
		t.Fatalf("inside grace: state %s, want delivered", tr.State)
	}

	fx.clock = fx.clock.Add(10 * time.Minute)
	if tr = step(t, r, in.ID, false); tr.State != faults.TriageSpawned {
		t.Fatalf("after grace: state %s, want filed and spawned", tr.State)
	}
}

func TestRoute_FixSessionFaultIsRecordedNeverSpawned(t *testing.T) {
	r, fx, _ := harness(t)
	first := raise(t, "a", 0, 0)
	step(t, r, first.ID, false) // files and spawns E-901
	fx.sessions[8] = sessionInfo{found: true, live: true, idle: true, pane: "%9", taskID: 901}

	second := raise(t, "b", 901, 8)
	tr := step(t, r, second.ID, false)
	if tr.State != faults.TriageRecorded || tr.FixTaskID != 901 {
		t.Fatalf("triage = %+v, want recorded on E-901", tr)
	}
	if len(fx.filed) != 1 || len(fx.spawned) != 1 || len(fx.delivered) != 0 {
		t.Fatalf("filed %d spawned %d delivered %d; want 1, 1, 0", len(fx.filed), len(fx.spawned), len(fx.delivered))
	}
	if !strings.Contains(fx.contexts[901], "raised by this fix task's own work") {
		t.Errorf("E-901's context does not record it:\n%s", fx.contexts[901])
	}
}

func TestRoute_RecurrenceJoinsTheOpenFixTask(t *testing.T) {
	r, fx, _ := harness(t)
	first := raise(t, "a", 0, 0)
	step(t, r, first.ID, false)
	if _, err := faults.Clear(faults.AllProjects, []int64{first.ID}, "test"); err != nil {
		t.Fatal(err)
	}

	again := raise(t, "a", 0, 0)
	if again.ID == first.ID {
		t.Fatal("the recurrence should be a new incident")
	}
	tr := step(t, r, again.ID, false)
	if tr.State != faults.TriageLinked || tr.FixTaskID != 901 {
		t.Fatalf("triage = %+v, want linked to E-901", tr)
	}
	if len(fx.filed) != 1 {
		t.Fatalf("filed %d tasks, want 1: one task per fingerprint", len(fx.filed))
	}
	if ctx := fx.contexts[901]; !strings.Contains(ctx, "## Error "+itoa(first.ID)) || !strings.Contains(ctx, "## Error "+itoa(again.ID)) {
		t.Errorf("E-901's context should list both incidents:\n%s", ctx)
	}
}

func TestRoute_RecurrenceAfterASettledFixFilesANewOneCleaningItUp(t *testing.T) {
	r, fx, _ := harness(t)
	first := raise(t, "a", 0, 0)
	step(t, r, first.ID, false)
	if _, err := faults.Clear(faults.AllProjects, []int64{first.ID}, "test"); err != nil {
		t.Fatal(err)
	}
	fx.settled[901] = true

	again := raise(t, "a", 0, 0)
	tr := step(t, r, again.ID, false)
	if tr.FixTaskID != 902 || tr.State != faults.TriageSpawned {
		t.Fatalf("triage = %+v, want a new E-902, spawned", tr)
	}
	if got := fx.filed[1].cleansUp; len(got) != 1 || got[0] != 901 {
		t.Errorf("cleansUp = %v, want the settled E-901", got)
	}
}

func TestRoutables_OnlyAfterTheOptInWatermark(t *testing.T) {
	_, _, db := harness(t)
	if _, err := db.Exec(`INSERT INTO projects (id, name, path) VALUES (1, 'p', '/p')`); err != nil {
		t.Fatal(err)
	}
	faults.Record(faults.Fault{Code: faults.ErrCodeJobFailed, Source: "s", Summary: "before", ProjectID: 1})
	if _, err := db.Exec(`UPDATE errors SET first_seen_at = '2000-01-01T00:00:00'`); err != nil {
		t.Fatal(err)
	}
	if err := faults.SyncTriageProjects([]int64{1}); err != nil {
		t.Fatal(err)
	}
	faults.Record(faults.Fault{Code: faults.ErrCodeJobFailed, Source: "s", Summary: "after", ProjectID: 1})

	routables, err := faults.Routables([]int64{1})
	if err != nil {
		t.Fatal(err)
	}
	if len(routables) != 1 || routables[0].Incident.Summary != "after" {
		t.Fatalf("routables = %+v, want only the incident after opt-in", routables)
	}

	// Opting out drops the watermark; opting back in starts a new one.
	if err = faults.SyncTriageProjects(nil); err != nil {
		t.Fatal(err)
	}
	if routables, _ = faults.Routables([]int64{1}); len(routables) != 0 {
		t.Fatalf("opted out, still routable: %+v", routables)
	}
}

func TestParseDelivery(t *testing.T) {
	for out, want := range map[string]delivery{
		"Sent it.\nDELIVERED":             delivered,
		"done\n\n**DELIVERED**\n":         delivered,
		"The message is held.\nHELD":      held,
		"NOT_FOUND":                       notFound,
		"I sent the message successfully": failed,
		"":                                failed,
	} {
		if got := parseDelivery(out); got != want {
			t.Errorf("parseDelivery(%q) = %s, want %s", out, got, want)
		}
	}
}

func TestSenderPrompt_NamesThePaneAndCarriesTheMessage(t *testing.T) {
	p := senderPrompt("%373", "hello there")
	for _, want := range []string{"%373", "ListAgents", "SendMessage", "hello there", "HELD"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
