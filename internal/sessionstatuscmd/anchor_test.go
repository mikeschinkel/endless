package sessionstatuscmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// The E-1892 lifecycle tests. `session monitor` used to resolve its focal task
// once, before entering its redraw loop, so a monitor started before its session
// registered — the common case under E-1851's spawned layout, where the pane is
// created milliseconds before Claude's hook writes the session row — showed the
// claim/bind hint forever. anchorTracker re-resolves each tick until a focal task
// appears, then freezes on it.

// scriptedResolver returns each anchor in seq in order (repeating the last one
// once exhausted) and counts how many times it was called, so a test can assert
// both what the tracker picked up and that it STOPPED asking.
type scriptedResolver struct {
	seq   []anchor
	errs  []error
	calls int
}

func (s *scriptedResolver) resolve() (anchor, error) {
	i := s.calls
	s.calls++
	if i >= len(s.seq) {
		i = len(s.seq) - 1
	}
	var err error
	if i < len(s.errs) {
		err = s.errs[i]
	}
	return s.seq[i], err
}

// TestAnchorTrackerReresolvesUntilFocalAppears is the core fix: three empty
// resolutions in a row must not be final — the tick that finally resolves a
// focal task is picked up.
func TestAnchorTrackerReresolvesUntilFocalAppears(t *testing.T) {
	r := &scriptedResolver{seq: []anchor{
		{hint: hintNoSession},
		{hint: hintNoSession},
		{hint: hintNoSession},
		{focal: 1851, hint: hintClaimBind},
	}}
	tr := newAnchorTracker(r.resolve)

	for tick := 1; tick <= 3; tick++ {
		if got := tr.refresh().focal; got != 0 {
			t.Fatalf("tick %d: focal = %d, want 0 (session not registered yet)", tick, got)
		}
	}
	if got := tr.refresh().focal; got != 1851 {
		t.Errorf("focal = %d, want 1851 once the session row appears", got)
	}
}

// TestAnchorTrackerFreezesAfterFirstHit pins E-1698's anchor-once contract: once
// a task is anchored the view stays pinned to THIS window's task as other
// sessions come and go, so the tracker must stop calling the resolver entirely.
func TestAnchorTrackerFreezesAfterFirstHit(t *testing.T) {
	r := &scriptedResolver{seq: []anchor{
		{hint: hintNoSession},
		{focal: 1851, hint: hintClaimBind},
		// Would re-anchor onto an unrelated task if the tracker kept asking.
		{focal: 9999, hint: hintClaimBind},
	}}
	tr := newAnchorTracker(r.resolve)

	tr.refresh() // focal 0
	tr.refresh() // focal 1851 — anchored
	frozen := r.calls

	for tick := 0; tick < 5; tick++ {
		if got := tr.refresh().focal; got != 1851 {
			t.Fatalf("focal = %d after anchoring, want 1851 (must never re-anchor)", got)
		}
	}
	if r.calls != frozen {
		t.Errorf("resolver called %d more times after anchoring, want 0", r.calls-frozen)
	}
}

// TestAnchorTrackerUpdatesWholeSetOnTheSameTick: focal, parentSession and
// emittingSession are read from the same not-yet-settled state and share one
// race, so they must land together. A parentSession arriving one tick late would
// drop the ↩ from row from the first painted frame.
func TestAnchorTrackerUpdatesWholeSetOnTheSameTick(t *testing.T) {
	r := &scriptedResolver{seq: []anchor{
		{emittingSession: 0, hint: hintNoSession},
		{focal: 1851, parentSession: 1026, emittingSession: 0, hint: hintClaimBind},
	}}
	tr := newAnchorTracker(r.resolve)

	tr.refresh()
	got := tr.refresh()
	if got.focal != 1851 || got.parentSession != 1026 {
		t.Errorf("anchor = {focal:%d parent:%d}, want {1851 1026} on the SAME tick",
			got.focal, got.parentSession)
	}
}

// TestAnchorTrackerKeepsResolvingEmittingSessionWhileUnanchored: emittingSession
// drives the no-goal view (E-1802) and is consulted only while focal == 0, so
// freezing it at a stale 0 would leave that view permanently empty for exactly
// the sessions it exists to serve.
func TestAnchorTrackerKeepsResolvingEmittingSessionWhileUnanchored(t *testing.T) {
	r := &scriptedResolver{seq: []anchor{
		{hint: hintNoSession},
		{emittingSession: 1026, hint: hintClaimBind},
	}}
	tr := newAnchorTracker(r.resolve)

	if got := tr.refresh().emittingSession; got != 0 {
		t.Fatalf("emittingSession = %d, want 0 before the session row exists", got)
	}
	got := tr.refresh()
	if got.emittingSession != 1026 {
		t.Errorf("emittingSession = %d, want 1026 once the session registers", got.emittingSession)
	}
	if got.focal != 0 {
		t.Errorf("focal = %d, want 0 (an unclaimed session must not anchor)", got.focal)
	}
}

// TestAnchorTrackerResolverErrorIsNonFatal: a resolver error while still
// unanchored must not end the monitor the way a render error does — the point of
// the retry is to outlast a transient nothing-here-yet. The previous anchor (and
// its hint) is kept and the next tick retries.
func TestAnchorTrackerResolverErrorIsNonFatal(t *testing.T) {
	boom := errors.New("db locked")
	r := &scriptedResolver{
		seq:  []anchor{{}, {focal: 1851, hint: hintClaimBind}},
		errs: []error{boom, nil},
	}
	tr := newAnchorTracker(r.resolve)

	got := tr.refresh()
	if got.focal != 0 {
		t.Errorf("focal = %d after a resolver error, want 0", got.focal)
	}
	if got.hint != hintClaimBind {
		t.Errorf("hint = %q after a resolver error, want the default %q", got.hint, hintClaimBind)
	}
	if got := tr.refresh().focal; got != 1851 {
		t.Errorf("focal = %d on the tick after an error, want 1851 (must retry)", got)
	}
}

// TestAnchorTrackerStartsWithTheClaimBindHint: before the first resolution the
// view has to render something, and it is the same claim/bind message the old
// pre-loop default used.
func TestAnchorTrackerStartsWithTheClaimBindHint(t *testing.T) {
	tr := newAnchorTracker(func() (anchor, error) { return anchor{}, errors.New("no tmux") })
	if got := tr.cur.hint; got != hintClaimBind {
		t.Errorf("initial hint = %q, want %q", got, hintClaimBind)
	}
}

// TestMonitorFrameRendersRowsOnceFocalAppears is the resolve→render composition
// the loop repeats: the same tracker that showed the hint must, on the tick its
// focal task resolves, render that task's rows. This is what a blank monitor
// pane looked like from the user's side.
func TestMonitorFrameRendersRowsOnceFocalAppears(t *testing.T) {
	prevRows := gatherRows
	t.Cleanup(func() { gatherRows = prevRows })
	gatherRows = func(focal, parentSession, emittingSession int64, all bool) ([]monitor.SessionStatusRow, error) {
		if focal == 0 {
			return nil, nil
		}
		return []monitor.SessionStatusRow{
			{ID: focal, Title: "Focal probe task", Status: "underway", Phase: "now", TypeSlug: "todo", IsFocal: true},
			{ID: 1892, Title: "Child probe task", Status: "ready", Phase: "now", TypeSlug: "todo"},
		}, nil
	}

	r := &scriptedResolver{seq: []anchor{
		{hint: hintNoSession},
		{focal: 1851, hint: hintClaimBind},
	}}
	tr := newAnchorTracker(r.resolve)

	var before strings.Builder
	nBefore, err := monitorFrame(tr, &before, false, 90, false, hiddenOmit)
	if err != nil {
		t.Fatalf("monitorFrame (unresolved): %v", err)
	}
	if !strings.Contains(before.String(), "register it") {
		t.Errorf("unresolved frame should carry the resolved no-session hint:\n%s", before.String())
	}
	// 0 rows is what holds the pane at monitorPaneEmptyHeight (E-1851) rather
	// than exact-fitting the hint.
	if nBefore != 0 {
		t.Errorf("unresolved frame reported %d rows, want 0", nBefore)
	}

	var after strings.Builder
	nAfter, err := monitorFrame(tr, &after, false, 90, false, hiddenOmit)
	if err != nil {
		t.Fatalf("monitorFrame (resolved): %v", err)
	}
	// The row count flipping 0 → non-zero in this same repaint is what regrows
	// the pane from the empty height to an exact fit.
	if nAfter != 2 {
		t.Errorf("resolved frame reported %d rows, want 2", nAfter)
	}
	out := after.String()
	if strings.Contains(out, "claim or bind") || strings.Contains(out, "register it") {
		t.Errorf("resolved frame still shows a no-task hint:\n%s", out)
	}
	for _, id := range []string{"E-1851", "E-1892"} {
		if !strings.Contains(out, id) {
			t.Errorf("row %s missing after the focal task resolved:\n%s", id, out)
		}
	}
}
