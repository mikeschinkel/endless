package sessionstatuscmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

// TestRelationGlyphWidths pins ⊕ and · at a single terminal column each, the
// same invariant TestActionIcons and TestHiddenGlyphWidth enforce for their
// glyphs. The relation column is a fixed-width slot in the row prefix; a
// double-width glyph would shove every following column right on marked rows
// only, raggedizing exactly the frames the tier exists to make readable.
func TestRelationGlyphWidths(t *testing.T) {
	for name, g := range map[string]string{
		"queuedGlyph":     queuedGlyph,
		"referencedGlyph": referencedGlyph,
	} {
		if w := runewidth.StringWidth(g); w != 1 {
			t.Errorf("%s %q measures %d columns, want 1", name, g, w)
		}
	}
}

// TestSortRows_ReferencedSinksBelowEverything is the flood-control guarantee.
// A `referenced` row goes last no matter how actionable its status makes it —
// here an `underway` in-flight task, which classifies to the TOP action rank and
// would otherwise lead the table. Without this, ten reads in a session push the
// work off the top of the pane.
func TestSortRows_ReferencedSinksBelowEverything(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 1, Title: "read", Status: "underway", Phase: "urgent", InFlight: true,
			Relation: sessiontaskrelation.RelationReferenced},
		{ID: 2, Title: "queued", Status: "ready", Phase: "later",
			Relation: sessiontaskrelation.RelationQueued},
		{ID: 3, Title: "revisited", Status: "ready", Phase: "maybe",
			Relation: sessiontaskrelation.RelationRevisited},
	}
	sortRows(rows)
	if rows[len(rows)-1].ID != 1 {
		t.Errorf("referenced row did not sink: order = %v", ids(rows))
	}
}

// TestSortRows_ProminenceIsOnlyATiebreak is the other half, and the one that
// keeps `session task add` from deranging the board: decided work wins ONLY
// against equally actionable rows. A `queued` task parked in `later` must stay
// below the row actually being worked.
func TestSortRows_ProminenceIsOnlyATiebreak(t *testing.T) {
	// Equal action (both plain `ready` → ▶ do): queued outranks revisited even
	// though its phase is worse and its id higher, both of which are lower keys.
	tie := []monitor.SessionStatusRow{
		{ID: 20, Title: "revisited", Status: "ready", Phase: "now",
			Relation: sessiontaskrelation.RelationRevisited},
		{ID: 21, Title: "queued", Status: "ready", Phase: "later",
			Relation: sessiontaskrelation.RelationQueued},
	}
	sortRows(tie)
	if tie[0].ID != 21 {
		t.Errorf("queued should lead an equal-action tie: order = %v", ids(tie))
	}

	// Unequal action: the in-flight `underway` row leads regardless of relation.
	// Relation must not outrank the action classification.
	mixed := []monitor.SessionStatusRow{
		{ID: 30, Title: "queued", Status: "ready", Phase: "later",
			Relation: sessiontaskrelation.RelationQueued},
		{ID: 31, Title: "doing", Status: "underway", Phase: "now", InFlight: true,
			Relation: sessiontaskrelation.RelationRevisited},
	}
	sortRows(mixed)
	if mixed[0].ID != 31 {
		t.Errorf("action must outrank relation: order = %v", ids(mixed))
	}
}

// TestSortRows_UnclassifiedRowsRankWithIncidental pins that read-time rows —
// the focal task's children, dependents and upstream blockers, which carry NO
// session_tasks row and so no relation — are NOT demoted below surfaced or
// revisited work. They are real work on the focal task; only their bookkeeping
// differs, and sinking them would reorder the board for no reason a viewer could
// see. Equal prominence means the pre-E-1696 phase→id ordering still decides.
func TestSortRows_UnclassifiedRowsRankWithIncidental(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 41, Title: "revisited", Status: "ready", Phase: "next",
			Relation: sessiontaskrelation.RelationRevisited},
		{ID: 40, Title: "child, no relation", Status: "ready", Phase: "now"},
	}
	sortRows(rows)
	// `now` beats `next`, so the unclassified child leads on PHASE — proving
	// relation did not sink it first.
	if rows[0].ID != 40 {
		t.Errorf("unclassified row was demoted below revisited: order = %v", ids(rows))
	}
}

// TestRelationColumn_WidthOnDemand pins the no-regression promise: a frame whose
// rows carry no queued/referenced mark must render byte-identically to one whose
// rows have no relation at all. The conditional column is the whole reason
// E-1696 does not change every existing frame.
func TestRelationColumn_WidthOnDemand(t *testing.T) {
	plain := []monitor.SessionStatusRow{
		{ID: 1, Title: "a", Status: "ready", Phase: "now", TypeSlug: "todo"},
	}
	classified := []monitor.SessionStatusRow{
		{ID: 1, Title: "a", Status: "ready", Phase: "now", TypeSlug: "todo",
			Relation: sessiontaskrelation.RelationRevisited},
	}
	if got, want := render(plain), render(classified); got != want {
		t.Errorf("unmarked relation changed the frame:\n got: %q\nwant: %q", got, want)
	}
	if strings.Contains(render(plain), queuedGlyph) {
		t.Error("unmarked frame carries the queued glyph")
	}
}

// TestRelationColumn_MarksQueuedAndReferenced covers the visible half: once any
// row earns a mark the column appears, each marked row wears its own glyph, and
// the legend names both so the frame is self-documenting.
func TestRelationColumn_MarksQueuedAndReferenced(t *testing.T) {
	out := render([]monitor.SessionStatusRow{
		{ID: 1, Title: "queued work", Status: "ready", Phase: "now", TypeSlug: "todo",
			Relation: sessiontaskrelation.RelationQueued},
		{ID: 2, Title: "just read", Status: "ready", Phase: "now", TypeSlug: "todo",
			Relation: sessiontaskrelation.RelationReferenced},
	})
	for _, want := range []string{
		queuedGlyph + " queued", referencedGlyph + " referenced",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("legend missing %q:\n%s", want, out)
		}
	}
}

// TestColorize_ReferencedDims pins that a referenced row renders dim, and that
// E-1707's unsettled veto covers it on the same terms as every other dim case —
// which is the reason it joined the existing dim set instead of getting its own
// arm.
func TestColorize_ReferencedDims(t *testing.T) {
	const line = "· E-1 read-only"
	ref := monitor.SessionStatusRow{
		Status: "ready", Phase: "now",
		Relation: sessiontaskrelation.RelationReferenced,
	}
	if got, want := colorize(line, ref, true), ansiDim+line+ansiReset; got != want {
		t.Errorf("referenced row not dimmed: got %q, want %q", got, want)
	}

	ref.Unsettled = true
	if got := colorize(line, ref, true); got != line {
		t.Errorf("unsettled must veto the referenced dim: got %q, want %q", got, line)
	}
}

// render is the shared one-frame helper: no focal task, no hides, fixed width,
// color off so assertions read the plain text.
func render(rows []monitor.SessionStatusRow) string {
	var b strings.Builder
	renderTo(&b, rows, 0, hintClaimBind, 90, false, hiddenOmit)
	return b.String()
}

// ids renders a row order compactly for failure messages.
func ids(rows []monitor.SessionStatusRow) []int64 {
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// TestRenderJSON_CarriesRelation pins the data surface (E-1696): each row
// carries its relation slug, a row the viewer has no session_tasks row for omits
// the key entirely rather than emitting a debug form, and — the part a
// regression would silently break — --json orders rows the same way the table
// does. sortRows keys on relation, so a JSON path that skipped the annotation
// would sort by a field it never filled.
func TestRenderJSON_CarriesRelation(t *testing.T) {
	prevGather, prevHidden, prevRelation := gatherRows, annotateHidden, annotateRelation
	t.Cleanup(func() {
		gatherRows, annotateHidden, annotateRelation = prevGather, prevHidden, prevRelation
	})
	gatherRows = func(focal, parentSession, emittingSession int64, all bool) ([]monitor.SessionStatusRow, error) {
		return []monitor.SessionStatusRow{
			// Deliberately listed referenced-first: the sink must reorder it last.
			{ID: 1, Title: "read", Status: "ready", Phase: "now", TypeSlug: "todo"},
			{ID: 2, Title: "queued", Status: "ready", Phase: "now", TypeSlug: "todo"},
			{ID: 3, Title: "child", Status: "ready", Phase: "now", TypeSlug: "todo"},
		}, nil
	}
	annotateHidden = func(rows []monitor.SessionStatusRow, viewer int64) error { return nil }
	annotateRelation = func(rows []monitor.SessionStatusRow, viewer int64) error {
		rows[0].Relation = sessiontaskrelation.RelationReferenced
		rows[1].Relation = sessiontaskrelation.RelationQueued
		// rows[2] deliberately left unclassified — a read-time child.
		return nil
	}

	var b strings.Builder
	if err := renderJSON(&b, anchor{emittingSession: 42}, false); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}

	var frame struct {
		Rows []struct {
			ID       int64  `json:"id"`
			Relation string `json:"relation"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(b.String()), &frame); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, b.String())
	}
	if len(frame.Rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(frame.Rows))
	}
	// Order: queued (decided) leads, unclassified child next, referenced sunk.
	wantOrder := []int64{2, 3, 1}
	for i, want := range wantOrder {
		if frame.Rows[i].ID != want {
			t.Errorf("row %d = E-%d, want E-%d (JSON order must match the table)",
				i, frame.Rows[i].ID, want)
		}
	}
	bySlug := map[int64]string{}
	for _, r := range frame.Rows {
		bySlug[r.ID] = r.Relation
	}
	for id, want := range map[int64]string{1: "referenced", 2: "queued", 3: ""} {
		if bySlug[id] != want {
			t.Errorf("E-%d relation = %q, want %q", id, bySlug[id], want)
		}
	}
}
