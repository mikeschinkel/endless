package sessionstate_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/sessionstate"
)

// ---------------------------------------------------------------------------
// The transition table.
//
// It is documentation of the WRITERS, not a guard, so these tests police what
// documentation can get wrong: an endpoint that is not a state, a state nothing
// writes, a state nothing can leave, and a Trigger that does not name anything.
// ---------------------------------------------------------------------------

// TestEveryEndpointIsAStateOrSentinel is the subset invariant, extended from
// the groups to the table. A typo'd endpoint would describe an edge that does
// not exist, which reads exactly like a missing edge and is much harder to find.
func TestEveryEndpointIsAStateOrSentinel(t *testing.T) {
	for _, tr := range sessionstate.Transitions() {
		if !sessionstate.Valid(tr.From) &&
			tr.From != sessionstate.NoState && tr.From != sessionstate.AnyState {
			t.Errorf("transition %q -> %q: %q is neither a state nor a sentinel",
				tr.From, tr.To, tr.From)
		}
		// To has no sentinel: every write lands the row in a real state.
		if !sessionstate.Valid(tr.To) {
			t.Errorf("transition %q -> %q: %q is not a state", tr.From, tr.To, tr.To)
		}
	}
}

// TestEveryStateIsWritten pins that something in the tree puts a session into
// each state. A state nothing writes is a state that can only arrive by hand —
// and "what actually writes `needs_input`?" being answered from a grep instead
// of from here is the mistake that filed E-2105.
//
// `needs_input` is now the one permitted exception, and the exception is
// deliberate rather than a gap in the table (E-2091). Its two writers — a row's
// initial value and the revive-an-ended-row CASE — both meant "this row exists
// and has done nothing", not "a person is being waited on", and both now write
// `idle`. So the state has no producer on purpose: it means exactly what the
// declaration gate says it means, and the rows still carrying it are surfaced on
// the attention board to be resolved rather than migrated away in the dark.
//
// The exception is a NAMED list, not a skip, so every other state still fails
// here the moment its last writer goes.
func TestEveryStateIsWritten(t *testing.T) {
	writerless := map[sessionstate.State]bool{sessionstate.NeedsInput: true}

	written := map[sessionstate.State]bool{}
	for _, tr := range sessionstate.Transitions() {
		written[tr.To] = true
	}
	for _, s := range sessionstate.Get(sessionstate.All) {
		switch {
		case written[s] && writerless[s]:
			t.Errorf("state %q is listed as having no writer, but the table names one "+
				"— drop it from the exception list", s)
		case !written[s] && !writerless[s]:
			t.Errorf("nothing writes state %q — either a writer is missing from the "+
				"table, or the state has no producer at all", s)
		}
	}
}

// TestEveryStateHasAWayOut is the other half: a state a session can enter and
// never leave strands the row. `ended` counts as having a way out because
// E-1686's revive is a real edge — without it an ended row that was wrong stayed
// invisible forever, which is the bug that edge exists to fix.
func TestEveryStateHasAWayOut(t *testing.T) {
	out := map[sessionstate.State]int{}
	for _, tr := range sessionstate.Transitions() {
		switch tr.From {
		case sessionstate.NoState:
			// a creation, not a way out of anything
		case sessionstate.AnyState:
			for _, s := range sessionstate.Get(sessionstate.All) {
				if s != tr.To {
					out[s]++
				}
			}
		default:
			out[tr.From]++
		}
	}
	for _, s := range sessionstate.Get(sessionstate.All) {
		if out[s] == 0 {
			t.Errorf("state %q has no outbound transition — a session that reaches "+
				"it can never leave", s)
		}
	}
}

// TestEveryTriggerNamesItsWriter enforces the one thing the Trigger column is
// for. A row whose trigger is prose without a `pkg.Func` in it documents that
// something happens, which is what the table already says by existing.
func TestEveryTriggerNamesItsWriter(t *testing.T) {
	for _, tr := range sessionstate.Transitions() {
		if tr.Trigger == "" {
			t.Errorf("transition %q -> %q has no trigger", tr.From, tr.To)
			continue
		}
		if !strings.Contains(tr.Trigger, ".") {
			t.Errorf("transition %q -> %q: trigger %q names no function — the column "+
				"exists to answer \"which code writes this?\"", tr.From, tr.To, tr.Trigger)
		}
	}
}

// TestNoDuplicateEdges keeps one edge one row. Two rows for the same (from, to)
// would split the writer list, and a reader who found the first would stop.
func TestNoDuplicateEdges(t *testing.T) {
	seen := map[[2]string]bool{}
	for _, tr := range sessionstate.Transitions() {
		key := [2]string{tr.From, tr.To}
		if seen[key] {
			t.Errorf("edge %q -> %q appears twice — merge the writers into one row",
				tr.From, tr.To)
		}
		seen[key] = true
	}
}

// TestTransitionsReturnsDefensiveCopy pins that a caller iterating the table
// cannot corrupt it for the next one.
func TestTransitionsReturnsDefensiveCopy(t *testing.T) {
	first := sessionstate.Transitions()
	if len(first) == 0 {
		t.Fatal("Transitions() is empty")
	}
	first[0].To = "CLOBBERED"

	if sessionstate.Transitions()[0].To == "CLOBBERED" {
		t.Error("Transitions returned a live slice: mutating it corrupted the table")
	}
}

// TestTableIsPinned pins every edge and its order. The Trigger prose is
// deliberately NOT pinned here — it is expected to gain writers as they are
// found, and pinning it would turn every honest addition into a test edit. The
// EDGES are the claim: this is the complete set of state changes a session row
// undergoes, and adding one is a decision, not a detail.
func TestTableIsPinned(t *testing.T) {
	want := [][2]string{
		{sessionstate.NoState, sessionstate.Idle},
		{sessionstate.NoState, sessionstate.Working},
		{sessionstate.AnyState, sessionstate.Working},
		{sessionstate.Idle, sessionstate.Working},
		{sessionstate.AnyState, sessionstate.Prompted},
		{sessionstate.Prompted, sessionstate.Working},
		{sessionstate.AnyState, sessionstate.Idle},
		{sessionstate.AnyState, sessionstate.Ended},
		{sessionstate.Ended, sessionstate.Idle},
	}
	var got [][2]string
	for _, tr := range sessionstate.Transitions() {
		got = append(got, [2]string{tr.From, tr.To})
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("transition edges = %v,\nwant %v", got, want)
	}
}
