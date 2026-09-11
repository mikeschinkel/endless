package sessionstate_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
	"github.com/mikeschinkel/endless/internal/sessionstate"
)

// ---------------------------------------------------------------------------
// Structural invariants — the reason the package exists.
//
// These are what make omission a build failure instead of a silent wrong
// answer. Adding a state without revisiting the groups it belongs to breaks one
// of these, which is the outcome the declaration gate's `default: return false`
// never had.
// ---------------------------------------------------------------------------

// TestEveryGroupIsSubsetOfAll catches a typo'd or stale constant in any
// grouping: a member that is not in the vocabulary can only be a mistake.
func TestEveryGroupIsSubsetOfAll(t *testing.T) {
	for _, g := range sessionstate.AllGroups() {
		for _, s := range sessionstate.Get(g) {
			if !sessionstate.Has(sessionstate.All, s) {
				t.Errorf("group %q contains %q, which is not in the vocabulary",
					sessionstate.GroupSlug(g), s)
			}
		}
	}
}

// TestNoGroupHasDuplicates guards the ordered groups in particular, where a
// duplicate would give one state two ranks.
func TestNoGroupHasDuplicates(t *testing.T) {
	for _, g := range sessionstate.AllGroups() {
		seen := map[string]bool{}
		for _, s := range sessionstate.Get(g) {
			if seen[s] {
				t.Errorf("group %q lists %q twice", sessionstate.GroupSlug(g), s)
			}
			seen[s] = true
		}
	}
}

// TestEveryGroupHasASlug pins that a Group constant cannot be added to the
// registry without a CLI name — the Python client addresses groups by slug, so
// a slugless group would be unreachable from Python.
func TestEveryGroupHasASlug(t *testing.T) {
	for _, g := range sessionstate.AllGroups() {
		if sessionstate.GroupSlug(g) == "" {
			t.Errorf("group %d has no slug", int(g))
		}
	}
	if got, want := len(sessionstate.AllGroupSlugs()), len(sessionstate.AllGroups()); got != want {
		t.Errorf("AllGroupSlugs() has %d entries, AllGroups() has %d — a slug is duplicated", got, want)
	}
}

// TestLiveIsAllMinusEnded pins the definitional relationship in the registry:
// a session is live exactly when it has not ended.
//
// It is asserted rather than computed, and that is the point. Live is written
// out as a membership list precisely so that adding a state forces someone to
// place it; if the group were derived as "All minus Ended" the placement would
// happen silently and this test would be a tautology. What it catches is the
// two halves drifting — a state added to All and forgotten here, which is
// twenty-nine SQL predicates quietly answering the old question.
func TestLiveIsAllMinusEnded(t *testing.T) {
	var want []string
	for _, s := range sessionstate.Get(sessionstate.All) {
		if s != sessionstate.Ended {
			want = append(want, s)
		}
	}
	got := sessionstate.Get(sessionstate.Live)
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Live = %v, want All minus ended = %v.\n"+
			"If a new state is genuinely not live, say so by adding it to Ended's "+
			"side of this test — do not leave it out of Live by omission.", got, want)
	}
}

// TestMayWriteIsASubsetOfLive pins the one relationship between the two policy
// groups that cannot be otherwise: an ended session may not write files. If
// this fails, the declaration gate is admitting a session that is over.
func TestMayWriteIsASubsetOfLive(t *testing.T) {
	for _, s := range sessionstate.Get(sessionstate.MayWrite) {
		if !sessionstate.Has(sessionstate.Live, s) {
			t.Errorf("state %q may write but is not live", s)
		}
	}
}

// TestAwaitingAHumanAndMayWriteOverlapOnlyMidTurn pins the two states that are
// in BOTH policy groups, and the reason both are there is the same one.
//
// A session is admitted to write when it is mid-turn; it awaits a human when it
// has paused for input. `idle` and `prompted` are both at once. `idle` because a
// write from an idle session is mid-turn by construction — writes only happen
// inside turns, so the state is stale, not the agent (E-2093) — while the board
// still ranks it as a claim on attention (E-1976). `prompted` because the
// session is blocked on a permission answer for a tool call it has already
// decided to make (E-2091).
//
// `needs_input` is where the groups agree — awaiting a human AND refused — and
// that agreement is the invariant worth holding: a state that awaits a human
// must not be admitted unless something else says it is acting. A new state in
// both needs that "something else" written down, the way these two have it.
func TestAwaitingAHumanAndMayWriteOverlapOnlyMidTurn(t *testing.T) {
	var both []string
	for _, s := range sessionstate.Get(sessionstate.AwaitsHuman) {
		if sessionstate.Has(sessionstate.MayWrite, s) {
			both = append(both, s)
		}
	}
	want := []string{sessionstate.Prompted, sessionstate.Idle}
	if !reflect.DeepEqual(both, want) {
		t.Errorf("MayWrite ∩ AwaitsHuman = %v, want %v — a new state in both needs "+
			"the mid-turn argument `idle` and `prompted` have, written down", both, want)
	}
}

// TestPromptedMayWrite states E-2091's load-bearing decision on its own, rather
// than leaving it to be inferred from the pinned membership above.
//
// It is separate because its failure mode is separate. A wrong glyph is
// cosmetic; `prompted` falling out of MayWrite wedges every prompt-blocked
// session — the declaration gate refuses its next write the moment the user
// approves, and tells it there is no command to run. That is the exact failure
// E-2093 spent a task cleaning up after, and it is what the near-miss recorded
// in this package's doc comment would have shipped.
func TestPromptedMayWrite(t *testing.T) {
	if !sessionstate.Has(sessionstate.MayWrite, sessionstate.Prompted) {
		t.Error("prompted is not in MayWrite: a session blocked on a permission " +
			"prompt will be refused its next write after the user approves")
	}
}

// TestAwaitsHumanIsEveryPausedState pins what E-2091 decided "waiting on the
// user" means: the session has PAUSED FOR INPUT, question or no question. All
// three paused states are members on that one rule, so a later edit that drops
// or adds one is deliberate rather than incidental.
func TestAwaitsHumanIsEveryPausedState(t *testing.T) {
	got := sessionstate.Get(sessionstate.AwaitsHuman)
	want := []string{sessionstate.Prompted, sessionstate.Idle, sessionstate.NeedsInput}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AwaitsHuman = %v, want %v", got, want)
	}
	for _, s := range []string{sessionstate.Working, sessionstate.Ended} {
		if sessionstate.Has(sessionstate.AwaitsHuman, s) {
			t.Errorf("state %q awaits a human, but it is not paused for input", s)
		}
	}
}

// TestDisplayOrderCoversAll pins that `session list`'s sort ranks every state.
// A state missing from the order gets NULL from the SQL CASE and sorts
// arbitrarily — a row that moves around the table for no visible reason.
func TestDisplayOrderCoversAll(t *testing.T) {
	for _, s := range sessionstate.Get(sessionstate.All) {
		if sessionstate.Rank(sessionstate.DisplayOrder, s) == sessionstate.NoRank {
			t.Errorf("state %q has no display rank — `session list` would sort it by NULL", s)
		}
	}
	if got, want := len(sessionstate.Get(sessionstate.DisplayOrder)),
		len(sessionstate.Get(sessionstate.All)); got != want {
		t.Errorf("DisplayOrder has %d members, the vocabulary has %d", got, want)
	}
}

// TestEveryStateHasLabelAndGlyph pins that adding a state forces both display
// forms, so a renderer never draws an empty cell or the unknown marker for a
// state that genuinely exists.
func TestEveryStateHasLabelAndGlyph(t *testing.T) {
	for _, s := range sessionstate.Get(sessionstate.All) {
		if sessionstate.Label(s) == "" {
			t.Errorf("state %q has no label", s)
		}
		if g := sessionstate.Glyph(s); g == sessionstate.UnknownGlyph {
			t.Errorf("state %q has no glyph — it renders as %s", s, g)
		}
	}
}

// TestGlyphsAreSingleWidth pins the property `session list`'s state column
// rests on: the column is exactly one character wide, so a two-column glyph
// shifts every following cell on that row alone and reads as a corrupt table
// rather than as a bad glyph choice. Measured with go-runewidth, the same
// library the renderers use, because the answer for a symbol like ⚠ differs
// between rune count and terminal columns.
func TestGlyphsAreSingleWidth(t *testing.T) {
	for _, s := range sessionstate.Get(sessionstate.All) {
		if w := runewidth.StringWidth(sessionstate.Glyph(s)); w != 1 {
			t.Errorf("state %q glyph %q measures %d columns, want 1",
				s, sessionstate.Glyph(s), w)
		}
	}
	if w := runewidth.StringWidth(sessionstate.UnknownGlyph); w != 1 {
		t.Errorf("UnknownGlyph %q measures %d columns, want 1",
			sessionstate.UnknownGlyph, w)
	}
}

// TestGlyphsAreDistinct keeps the glyph vocabulary readable. It matters more
// here than for task status: `session list`'s state column is exactly one
// character wide, so two states sharing a glyph are indistinguishable in the
// only place the glyph is shown.
func TestGlyphsAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, s := range sessionstate.Get(sessionstate.All) {
		g := sessionstate.Glyph(s)
		if prev, dup := seen[g]; dup {
			t.Errorf("states %q and %q share glyph %q", prev, s, g)
		}
		seen[g] = s
	}
	if _, taken := seen[sessionstate.UnknownGlyph]; taken {
		t.Errorf("a state uses %s, which is reserved for a state outside the vocabulary",
			sessionstate.UnknownGlyph)
	}
}

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

func TestGetReturnsMembersInGroupOrder(t *testing.T) {
	got := sessionstate.Get(sessionstate.DisplayOrder)
	want := []string{"prompted", "working", "needs_input", "idle", "ended"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Get(DisplayOrder) = %v, want %v", got, want)
	}
}

// TestGetReturnsDefensiveCopy pins that a caller mutating the result cannot
// corrupt the registry for every later reader in the process.
func TestGetReturnsDefensiveCopy(t *testing.T) {
	first := sessionstate.Get(sessionstate.Live)
	if len(first) == 0 {
		t.Fatal("Get(Live) is empty")
	}
	first[0] = "CLOBBERED"
	first = append(first, "EXTRA")
	_ = first

	second := sessionstate.Get(sessionstate.Live)
	if second[0] == "CLOBBERED" {
		t.Error("Get returned a live slice: mutating it corrupted the registry")
	}
	if sessionstate.Has(sessionstate.Live, "EXTRA") {
		t.Error("appending to Get's result leaked into the registry")
	}
}

func TestGetUnknownGroupIsNil(t *testing.T) {
	if got := sessionstate.Get(sessionstate.Group(9999)); got != nil {
		t.Errorf("Get(bogus) = %v, want nil", got)
	}
}

func TestHas(t *testing.T) {
	cases := []struct {
		group sessionstate.Group
		s     string
		want  bool
	}{
		{sessionstate.MayWrite, sessionstate.Working, true},
		{sessionstate.MayWrite, sessionstate.Prompted, true},
		{sessionstate.MayWrite, sessionstate.Idle, true},
		{sessionstate.MayWrite, sessionstate.NeedsInput, false},
		{sessionstate.MayWrite, sessionstate.Ended, false},
		{sessionstate.Live, sessionstate.Prompted, true},
		{sessionstate.Live, sessionstate.Ended, false},
		{sessionstate.AwaitsHuman, sessionstate.Prompted, true},
		{sessionstate.AwaitsHuman, sessionstate.Working, false},
		{sessionstate.All, "nonsense", false},
		{sessionstate.Group(9999), sessionstate.Working, false},
	}
	for _, c := range cases {
		if got := sessionstate.Has(c.group, c.s); got != c.want {
			t.Errorf("Has(%s, %q) = %v, want %v",
				sessionstate.GroupSlug(c.group), c.s, got, c.want)
		}
	}
}

func TestSQLListQuotesAndJoins(t *testing.T) {
	if got, want := sessionstate.SQLList(sessionstate.MayWrite), "'working','prompted','idle'"; got != want {
		t.Errorf("SQLList(MayWrite) = %q, want %q", got, want)
	}
	if got := sessionstate.SQLList(sessionstate.Group(9999)); got != "" {
		t.Errorf("SQLList(bogus) = %q, want empty", got)
	}
}

// TestSQLListMatchesGet pins the two accessors against each other for every
// group, so a rendering bug cannot make one clause disagree with the Go-side
// membership check of the same rule.
func TestSQLListMatchesGet(t *testing.T) {
	for _, g := range sessionstate.AllGroups() {
		var want []string
		for _, s := range sessionstate.Get(g) {
			want = append(want, "'"+s+"'")
		}
		if got := sessionstate.SQLList(g); got != strings.Join(want, ",") {
			t.Errorf("SQLList(%s) = %q, disagrees with Get", sessionstate.GroupSlug(g), got)
		}
	}
}

// TestSQLListNeedsNoEscaping is the safety argument for SQLList stated as a
// test: the accessor interpolates into SQL without quoting because the
// vocabulary cannot contain a quote. A state that could would be an injection.
func TestSQLListNeedsNoEscaping(t *testing.T) {
	for _, s := range sessionstate.Get(sessionstate.All) {
		if strings.ContainsAny(s, "'\"\\") {
			t.Errorf("state %q contains a quote — SQLList interpolates it unescaped", s)
		}
	}
}

func TestRank(t *testing.T) {
	if got, want := sessionstate.Rank(sessionstate.DisplayOrder, sessionstate.Prompted), 0; got != want {
		t.Errorf("Rank(DisplayOrder, prompted) = %d, want %d", got, want)
	}
	if got, want := sessionstate.Rank(sessionstate.DisplayOrder, sessionstate.Working), 1; got != want {
		t.Errorf("Rank(DisplayOrder, working) = %d, want %d", got, want)
	}
	if got, want := sessionstate.Rank(sessionstate.DisplayOrder, sessionstate.Ended), 4; got != want {
		t.Errorf("Rank(DisplayOrder, ended) = %d, want %d", got, want)
	}
	if got := sessionstate.Rank(sessionstate.MayWrite, sessionstate.Ended); got != sessionstate.NoRank {
		t.Errorf("Rank(MayWrite, ended) = %d, want NoRank", got)
	}
}

func TestLabelAndGlyphUnknownState(t *testing.T) {
	if got := sessionstate.Label("nonsense"); got != "" {
		t.Errorf("Label(nonsense) = %q, want empty", got)
	}
	// Glyph deliberately differs from Label and from taskstatus.Glyph: the
	// unknown state has a defined rendering here, because the column it lands
	// in is one character wide and an empty cell would break the table.
	if got := sessionstate.Glyph("nonsense"); got != sessionstate.UnknownGlyph {
		t.Errorf("Glyph(nonsense) = %q, want %q", got, sessionstate.UnknownGlyph)
	}
	if got := sessionstate.Glyph(""); got != sessionstate.UnknownGlyph {
		t.Errorf("Glyph(\"\") = %q, want %q — this is how the Python client obtains "+
			"the marker without holding a copy", got, sessionstate.UnknownGlyph)
	}
}

func TestValidate(t *testing.T) {
	for _, s := range sessionstate.Get(sessionstate.All) {
		if err := sessionstate.Validate(s); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", s, err)
		}
	}
	err := sessionstate.Validate("waiting")
	if err == nil {
		t.Fatal("Validate(waiting) = nil, want an error")
	}
	// The message must name the vocabulary — an agent that guessed wrong should
	// learn the whole set in one round trip.
	if !strings.Contains(err.Error(), "needs_input") {
		t.Errorf("Validate error does not list the vocabulary: %v", err)
	}
}

// TestSentinelsAreNotStates pins that the transition table's two From sentinels
// stay outside the vocabulary. If either became a real state, Validate would
// start accepting it and every reader would inherit a value with no meaning.
func TestSentinelsAreNotStates(t *testing.T) {
	for _, sentinel := range []string{sessionstate.NoState, sessionstate.AnyState} {
		if sessionstate.Valid(sentinel) {
			t.Errorf("sentinel %q is a member of the vocabulary", sentinel)
		}
	}
}

func TestParseGroupRoundTrip(t *testing.T) {
	for _, g := range sessionstate.AllGroups() {
		slug := sessionstate.GroupSlug(g)
		got, err := sessionstate.ParseGroup(slug)
		if err != nil {
			t.Errorf("ParseGroup(%q) = %v", slug, err)
			continue
		}
		if got != g {
			t.Errorf("ParseGroup(%q) = %d, want %d", slug, int(got), int(g))
		}
	}
}

func TestParseGroupRejectsUnknown(t *testing.T) {
	_, err := sessionstate.ParseGroup("alive")
	if err == nil {
		t.Fatal("ParseGroup(alive) = nil error, want rejection")
	}
	if !strings.Contains(err.Error(), "live") {
		t.Errorf("ParseGroup error does not list the valid groups: %v", err)
	}
}

// TestGroupMembershipIsPinned pins the exact membership of every group. E-2105
// relocated these sets; it did not change them, and this is what proves it. A
// deliberate policy change edits this table AND says so; an accidental one
// fails here.
//
// Each row names the predicate it replaced, so the pin is checkable against the
// tree rather than against itself.
func TestGroupMembershipIsPinned(t *testing.T) {
	want := map[string][]string{
		// the vocabulary, in lifecycle order
		"all": {"working", "prompted", "idle", "needs_input", "ended"},
		// was `state != 'ended'`, 29 sites
		"live": {"working", "prompted", "idle", "needs_input"},
		// was hookcmd's `switch s.State { case stateWorking, stateIdle: }`;
		// `prompted` joined it by decision, not by default (E-2091)
		"may-write": {"working", "prompted", "idle"},
		// every state in which the session has paused for input (E-2091),
		// blocked-mid-turn first
		"awaits-human": {"prompted", "idle", "needs_input"},
		// was session_cmd.py's `CASE s.state WHEN 'working' THEN 0 ...`;
		// `prompted` sorts above `working` because it is the row that wants a
		// person (E-2091)
		"display-order": {"prompted", "working", "needs_input", "idle", "ended"},
	}
	for _, g := range sessionstate.AllGroups() {
		slug := sessionstate.GroupSlug(g)
		exp, ok := want[slug]
		if !ok {
			t.Errorf("group %q is not pinned here — add its membership", slug)
			continue
		}
		if got := sessionstate.Get(g); !reflect.DeepEqual(got, exp) {
			t.Errorf("group %q = %v, want %v", slug, got, exp)
		}
		delete(want, slug)
	}
	for slug := range want {
		t.Errorf("group %q is pinned here but no longer exists in the registry", slug)
	}
}

// TestLabelsAndGlyphsArePinned pins the display forms E-2105 relocated from
// session_cmd.py. The glyphs in particular moved verbatim, and `session list`'s
// output is asserted byte-identical across the change — this is the Go half of
// that proof.
func TestLabelsAndGlyphsArePinned(t *testing.T) {
	want := map[string][2]string{
		sessionstate.Working:    {"Working", "⟳"},
		sessionstate.Prompted:   {"Prompted", "⚠"},
		sessionstate.Idle:       {"Idle", "‖"},
		sessionstate.NeedsInput: {"Needs Input", "?"},
		sessionstate.Ended:      {"Ended", "␥"},
	}
	for _, s := range sessionstate.Get(sessionstate.All) {
		exp, ok := want[s]
		if !ok {
			t.Errorf("state %q has no pinned label/glyph — add it", s)
			continue
		}
		if got := sessionstate.Label(s); got != exp[0] {
			t.Errorf("Label(%q) = %q, want %q", s, got, exp[0])
		}
		if got := sessionstate.Glyph(s); got != exp[1] {
			t.Errorf("Glyph(%q) = %q, want %q", s, got, exp[1])
		}
		delete(want, s)
	}
	for s := range want {
		t.Errorf("state %q is pinned here but no longer exists in the registry", s)
	}
}

// TestLegendRendersAsSessionListPrintsIt pins the exact string
// `session list` puts under its table, composed the way session_cmd.py composes
// it — glyph, space, lowercased label, joined by three spaces, in All order.
//
// It lives here rather than only in the Python tests because the registry is
// what would break it: a relabelled state silently rewrites a line users read
// on every listing.
func TestLegendRendersAsSessionListPrintsIt(t *testing.T) {
	var parts []string
	for _, s := range sessionstate.Get(sessionstate.All) {
		parts = append(parts, sessionstate.Glyph(s)+" "+strings.ToLower(sessionstate.Label(s)))
	}
	got := strings.Join(parts, "   ")
	want := "⟳ working   ⚠ prompted   ‖ idle   ? needs input   ␥ ended"
	if got != want {
		t.Errorf("session list legend = %q, want %q", got, want)
	}
}
