package hookcmd

import "testing"

// TestRelayVerdictAnyOf_AcceptsAnyMember pins the forward-compatibility E-1975
// bought while it was one loop.
//
// Under the SHIPPED A/B shape the agent relays the combined block, so a
// single-text comparison would do. It is the only blocker between that shape and
// both later ones — an AskUserQuestion preview where the agent sends the WINNING
// variant, and a left/right TUI — and paying for it now costs a slice instead of
// a schema change later.
func TestRelayVerdictAnyOf_AcceptsAnyMember(t *testing.T) {
	combined := "─[Option A of B]───\nreply A\n─[Option B of B]───\nreply B\n───"
	accepted := []string{combined, "reply A", "reply B"}

	for _, actual := range accepted {
		if _, ok := relayVerdictAnyOf(accepted, actual); !ok {
			t.Errorf("a sanctioned text was rejected: %q", actual)
		}
	}
}

// TestRelayVerdictAnyOf_StillCatchesEmbellishment is the property that must not
// be weakened by any-of-N: relaying variant B plus a sentence is still the exact
// habit the gate exists to catch.
func TestRelayVerdictAnyOf_StillCatchesEmbellishment(t *testing.T) {
	accepted := []string{"reply A", "reply B"}
	extra, ok := relayVerdictAnyOf(accepted, "reply B\n\nI also refactored three files.")
	if ok {
		t.Fatal("an embellished variant was allowed through")
	}
	if extra != 1 {
		t.Errorf("extra = %d, want 1 appended line", extra)
	}
}

// TestRelayVerdictAnyOf_ReportsTheSmallestDivergence pins how the bounce reason
// is chosen.
//
// An agent that added one line to the SHORT option must be told it added one
// line, not forty. A bounce reason that misdescribes the violation teaches the
// wrong correction, and this gate cannot afford a correction the agent cannot
// act on.
func TestRelayVerdictAnyOf_ReportsTheSmallestDivergence(t *testing.T) {
	short := "one line"
	long := "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8"
	extra, ok := relayVerdictAnyOf([]string{long, short}, short+"\nand one more")
	if ok {
		t.Fatal("expected a block")
	}
	if extra != 1 {
		t.Errorf("extra = %d, want the smallest divergence (1)", extra)
	}
}

// An empty accepted set cannot prove a violation, so it must fail OPEN like
// every other case the gate cannot decide. Blocking here would hold a turn
// hostage over the gate's own ignorance.
func TestRelayVerdictAnyOf_EmptySetFailsOpen(t *testing.T) {
	if _, ok := relayVerdictAnyOf(nil, "anything at all"); !ok {
		t.Error("an empty accepted set blocked the turn")
	}
}

// The single-text path is unchanged, and relayVerdict must stay a thin wrapper
// over it — the E-1953 behavior is what every ordinary turn still takes.
func TestRelayVerdict_SingleTextUnchanged(t *testing.T) {
	if _, ok := relayVerdict("Verify: `just test`", "Verify: `just test`"); !ok {
		t.Error("a verbatim relay was blocked")
	}
	if extra, ok := relayVerdict("Verify: `just test`",
		"Verify: `just test`\n\nAlso I did other things.\nAnd more."); ok {
		t.Error("an embellished relay was allowed")
	} else if extra != 2 {
		t.Errorf("extra = %d, want 2", extra)
	}
}
