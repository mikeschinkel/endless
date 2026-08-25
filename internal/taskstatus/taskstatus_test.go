package taskstatus_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// ---------------------------------------------------------------------------
// Structural invariants — the reason the package exists.
//
// These are what make omission a build failure instead of a silent wrong
// answer. Adding a status without revisiting the groups it belongs to breaks
// one of these, which is the outcome E-1648 and E-1845 both needed and neither
// had.
// ---------------------------------------------------------------------------

// TestEveryGroupIsSubsetOfAll catches a typo'd or stale constant in any
// grouping: a member that is not in the vocabulary can only be a mistake.
func TestEveryGroupIsSubsetOfAll(t *testing.T) {
	for _, g := range taskstatus.AllGroups() {
		for _, s := range taskstatus.Get(g) {
			if !taskstatus.Has(taskstatus.All, s) {
				t.Errorf("group %q contains %q, which is not in the vocabulary",
					taskstatus.GroupSlug(g), s)
			}
		}
	}
}

// TestNoGroupHasDuplicates guards the ordered groups in particular, where a
// duplicate would give one status two ranks.
func TestNoGroupHasDuplicates(t *testing.T) {
	for _, g := range taskstatus.AllGroups() {
		seen := map[string]bool{}
		for _, s := range taskstatus.Get(g) {
			if seen[s] {
				t.Errorf("group %q lists %q twice", taskstatus.GroupSlug(g), s)
			}
			seen[s] = true
		}
	}
}

// TestEveryGroupHasASlug pins that a Group constant cannot be added to the
// registry without a CLI name — the Python client addresses groups by slug, so
// a slugless group would be unreachable from Python.
func TestEveryGroupHasASlug(t *testing.T) {
	for _, g := range taskstatus.AllGroups() {
		if taskstatus.GroupSlug(g) == "" {
			t.Errorf("group %d has no slug", int(g))
		}
	}
	if got, want := len(taskstatus.AllGroupSlugs()), len(taskstatus.AllGroups()); got != want {
		t.Errorf("AllGroupSlugs() has %d entries, AllGroups() has %d — a slug is duplicated", got, want)
	}
}

// partition asserts that the named groups/statuses cover All exactly once each.
func partition(t *testing.T, what string, parts ...[]string) {
	t.Helper()
	count := map[string]int{}
	for _, part := range parts {
		for _, s := range part {
			count[s]++
		}
	}
	for _, s := range taskstatus.Get(taskstatus.All) {
		switch count[s] {
		case 1:
		case 0:
			t.Errorf("%s: %q is in no part — it would be silently dropped", what, s)
		default:
			t.Errorf("%s: %q is in %d parts — it would be counted twice", what, s, count[s])
		}
		delete(count, s)
	}
}

// TestActionablePartitionsAll pins that `task next`'s exclusion list and the
// set of statuses it may offer are complements. Without this a new status lands
// in neither and quietly picks a side depending on whether the SQL says IN or
// NOT IN.
func TestActionablePartitionsAll(t *testing.T) {
	partition(t, "actionable|not-actionable",
		taskstatus.Get(taskstatus.Actionable),
		taskstatus.Get(taskstatus.NotActionable),
	)
}

// TestChildrenStateOrderPartitionsAll is symptom 2 of E-1891, made structural.
// `submitted` was missing from the children-state display order while still
// being counted in the "(N total)" suffix, so the breakdown did not reconcile —
// it rendered no bucket for a submitted child. Any status missing from the
// order, or double-counted across the order and the collapsed terminal bucket,
// breaks the total the same way. This test is why it cannot happen again.
func TestChildrenStateOrderPartitionsAll(t *testing.T) {
	partition(t, "children-state-order|terminal",
		taskstatus.Get(taskstatus.ChildrenStateOrder),
		taskstatus.Get(taskstatus.Terminal),
	)
}

// TestSessionDispositionsPartitionAll pins the session-status task rollup: its
// four buckets are Terminal, `blocked`, `unverified` and SessionPending. A
// status in none of them would fall through to Pending by accident rather than
// by decision.
func TestSessionDispositionsPartitionAll(t *testing.T) {
	partition(t, "session dispositions",
		taskstatus.Get(taskstatus.Terminal),
		[]string{taskstatus.Blocked},
		[]string{taskstatus.Unverified},
		taskstatus.Get(taskstatus.SessionPending),
	)
}

// TestSettledIsTerminalPlusUnverified pins the one relationship between groups
// that is definitional rather than coincidental: settled work is terminal work
// plus work awaiting verification.
func TestSettledIsTerminalPlusUnverified(t *testing.T) {
	want := append(taskstatus.Get(taskstatus.Terminal), taskstatus.Unverified)
	got := taskstatus.Get(taskstatus.Settled)
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Settled = %v, want Terminal+unverified = %v", got, want)
	}
}

// TestVerificationTrackIsItsTerminalsPlusUnverified pins the one other
// definitional relationship between groups: the verification track is the two
// ways user-testable work finishes, plus the gate they pass through.
func TestVerificationTrackIsItsTerminalsPlusUnverified(t *testing.T) {
	want := append(taskstatus.Get(taskstatus.VerificationTerminal), taskstatus.Unverified)
	got := taskstatus.Get(taskstatus.VerificationTrack)
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("VerificationTrack = %v, want VerificationTerminal+unverified = %v", got, want)
	}
}

// TestEveryStatusHasLabelAndGlyph pins that adding a status forces both display
// forms, so a renderer never draws an empty cell.
func TestEveryStatusHasLabelAndGlyph(t *testing.T) {
	for _, s := range taskstatus.Get(taskstatus.All) {
		if taskstatus.Label(s) == "" {
			t.Errorf("status %q has no label", s)
		}
		if taskstatus.Glyph(s) == "" {
			t.Errorf("status %q has no glyph", s)
		}
	}
}

// TestGlyphsAreDistinct keeps the glyph vocabulary readable: two statuses
// sharing a glyph is only distinguishable by color, and the registry owns the
// shape precisely because color is not shareable across media.
func TestGlyphsAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, s := range taskstatus.Get(taskstatus.All) {
		g := taskstatus.Glyph(s)
		if prev, dup := seen[g]; dup {
			t.Errorf("statuses %q and %q share glyph %q", prev, s, g)
		}
		seen[g] = s
	}
}

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

func TestGetReturnsMembersInGroupOrder(t *testing.T) {
	got := taskstatus.Get(taskstatus.DerivationPrecedence)
	want := []string{"underway", "ready", "submitted", "unplanned", "untriaged"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Get(DerivationPrecedence) = %v, want %v", got, want)
	}
}

// TestGetReturnsDefensiveCopy pins that a caller mutating the result cannot
// corrupt the registry for every later reader in the process.
func TestGetReturnsDefensiveCopy(t *testing.T) {
	first := taskstatus.Get(taskstatus.Terminal)
	if len(first) == 0 {
		t.Fatal("Get(Terminal) is empty")
	}
	first[0] = "CLOBBERED"
	first = append(first, "EXTRA")
	_ = first

	second := taskstatus.Get(taskstatus.Terminal)
	if second[0] == "CLOBBERED" {
		t.Error("Get returned a live slice: mutating it corrupted the registry")
	}
	if taskstatus.Has(taskstatus.Terminal, "EXTRA") {
		t.Error("appending to Get's result leaked into the registry")
	}
}

func TestGetUnknownGroupIsNil(t *testing.T) {
	if got := taskstatus.Get(taskstatus.Group(9999)); got != nil {
		t.Errorf("Get(bogus) = %v, want nil", got)
	}
}

func TestHas(t *testing.T) {
	cases := []struct {
		group taskstatus.Group
		s     string
		want  bool
	}{
		{taskstatus.SubmittableFrom, taskstatus.Untriaged, true},
		{taskstatus.SubmittableFrom, taskstatus.Ready, false},
		{taskstatus.Terminal, taskstatus.Unverified, false},
		{taskstatus.Shipped, taskstatus.Unverified, true},
		{taskstatus.All, "nonsense", false},
		{taskstatus.Group(9999), taskstatus.Ready, false},
	}
	for _, c := range cases {
		if got := taskstatus.Has(c.group, c.s); got != c.want {
			t.Errorf("Has(%s, %q) = %v, want %v",
				taskstatus.GroupSlug(c.group), c.s, got, c.want)
		}
	}
}

func TestSQLListQuotesAndJoins(t *testing.T) {
	if got, want := taskstatus.SQLList(taskstatus.PreJudgment), "'untriaged','unplanned'"; got != want {
		t.Errorf("SQLList(PreJudgment) = %q, want %q", got, want)
	}
	if got := taskstatus.SQLList(taskstatus.Group(9999)); got != "" {
		t.Errorf("SQLList(bogus) = %q, want empty", got)
	}
}

// TestSQLListMatchesGet pins the two accessors against each other for every
// group, so a rendering bug cannot make one clause disagree with the Go-side
// membership check of the same rule.
func TestSQLListMatchesGet(t *testing.T) {
	for _, g := range taskstatus.AllGroups() {
		var want []string
		for _, s := range taskstatus.Get(g) {
			want = append(want, "'"+s+"'")
		}
		if got := taskstatus.SQLList(g); got != strings.Join(want, ",") {
			t.Errorf("SQLList(%s) = %q, disagrees with Get", taskstatus.GroupSlug(g), got)
		}
	}
}

func TestRank(t *testing.T) {
	if got, want := taskstatus.Rank(taskstatus.DerivationPrecedence, taskstatus.Underway), 0; got != want {
		t.Errorf("Rank(DerivationPrecedence, underway) = %d, want %d", got, want)
	}
	if got, want := taskstatus.Rank(taskstatus.DerivationPrecedence, taskstatus.Untriaged), 4; got != want {
		t.Errorf("Rank(DerivationPrecedence, untriaged) = %d, want %d", got, want)
	}
	if got := taskstatus.Rank(taskstatus.DerivationPrecedence, taskstatus.Confirmed); got != taskstatus.NoRank {
		t.Errorf("Rank(DerivationPrecedence, confirmed) = %d, want NoRank", got)
	}
}

func TestLabelAndGlyphUnknownStatus(t *testing.T) {
	if got := taskstatus.Label("nonsense"); got != "" {
		t.Errorf("Label(nonsense) = %q, want empty", got)
	}
	if got := taskstatus.Glyph("nonsense"); got != "" {
		t.Errorf("Glyph(nonsense) = %q, want empty", got)
	}
}

func TestValidate(t *testing.T) {
	for _, s := range taskstatus.Get(taskstatus.All) {
		if err := taskstatus.Validate(s); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", s, err)
		}
	}
	err := taskstatus.Validate("in_progress")
	if err == nil {
		t.Fatal("Validate(in_progress) = nil, want an error")
	}
	// The message must name the vocabulary — an agent that guessed wrong should
	// learn the whole set in one round trip.
	if !strings.Contains(err.Error(), "untriaged") {
		t.Errorf("Validate error does not list the vocabulary: %v", err)
	}
}

func TestParseGroupRoundTrip(t *testing.T) {
	for _, g := range taskstatus.AllGroups() {
		slug := taskstatus.GroupSlug(g)
		got, err := taskstatus.ParseGroup(slug)
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
	_, err := taskstatus.ParseGroup("terminals")
	if err == nil {
		t.Fatal("ParseGroup(terminals) = nil error, want rejection")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("ParseGroup error does not list the valid groups: %v", err)
	}
}

// TestGroupMembershipIsPinned pins the exact membership of every group. E-1891
// relocated these sets; it did not change them, and this is what proves it. A
// deliberate policy change edits this table AND says so; an accidental one
// fails here.
func TestGroupMembershipIsPinned(t *testing.T) {
	want := map[string][]string{
		"all":                    {"untriaged", "unplanned", "submitted", "ready", "underway", "unverified", "confirmed", "assumed", "completed", "blocked", "revisit", "declined", "obsolete"},
		"actionable":             {"unplanned", "ready", "revisit"},
		"not-actionable":         {"untriaged", "submitted", "underway", "unverified", "confirmed", "assumed", "completed", "blocked", "declined", "obsolete"},
		"active":                 {"underway", "unverified"},
		"claim-promotes":         {"untriaged", "unplanned", "ready", "blocked", "revisit"},
		"open":                   {"untriaged", "unplanned", "submitted", "ready", "underway"},
		"children-state-order":   {"untriaged", "unplanned", "submitted", "ready", "underway", "blocked", "revisit", "unverified"},
		"derivation-precedence":  {"underway", "ready", "submitted", "unplanned", "untriaged"},
		"description-reset-from": {"untriaged", "unplanned", "submitted", "ready", "revisit"},
		"pre-judgment":           {"untriaged", "unplanned"},
		"reopen-refused":         {"declined", "obsolete"},
		"reopenable":             {"confirmed", "assumed", "completed"},
		"session-pending":        {"untriaged", "unplanned", "submitted", "ready", "underway", "revisit"},
		"sets-completed-at":      {"confirmed", "completed"},
		"settled":                {"unverified", "confirmed", "assumed", "completed", "declined", "obsolete"},
		"shipped":                {"unverified", "confirmed", "assumed", "completed"},
		"sticky-override":        {"blocked", "revisit", "declined", "obsolete"},
		"submittable-from":       {"untriaged", "unplanned", "revisit"},
		"terminal":               {"confirmed", "assumed", "completed", "declined", "obsolete"},
		"verification-terminal":  {"confirmed", "assumed"},
		"verification-track":     {"unverified", "confirmed", "assumed"},
	}
	for _, g := range taskstatus.AllGroups() {
		slug := taskstatus.GroupSlug(g)
		exp, ok := want[slug]
		if !ok {
			t.Errorf("group %q is not pinned here — add its membership", slug)
			continue
		}
		if got := taskstatus.Get(g); !reflect.DeepEqual(got, exp) {
			t.Errorf("group %q = %v, want %v", slug, got, exp)
		}
		delete(want, slug)
	}
	for slug := range want {
		t.Errorf("group %q is pinned here but no longer exists", slug)
	}
}
