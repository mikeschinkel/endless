package hookcmd

import (
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// TestScanSigils_Recognized pins the vocabulary. Each case is a prompt a user
// would plausibly type, and the expectation is what the corpus should learn
// from it.
//
// E-1953's four fixed words are still here, but as EXAMPLES rather than as the
// grammar (ED-1555). What the parse now guarantees is the shape — line-leading
// sigil, a token, optional quoted spans — not the word list.
func TestScanSigils_Recognized(t *testing.T) {
	cases := []struct {
		name       string
		prompt     string
		wantLabels []monitor.ReportLabel
		wantFull   bool
		wantPick   string
		wantRate   int
	}{
		{
			name:       "a familiar token with text",
			prompt:     "$CUT you dropped the verify command",
			wantLabels: []monitor.ReportLabel{{Token: "CUT", Note: "you dropped the verify command"}},
		},
		{
			// The novel half. The user invents a word and scopes it to the exact
			// text that offended them, which is the sample the loop could never
			// get from a fixed vocabulary.
			name:   "an invented token scoped to a span",
			prompt: `$JARGON "load-bearing"`,
			wantLabels: []monitor.ReportLabel{
				{Token: "JARGON", Span: "load-bearing"},
			},
		},
		{
			// Two spans is two facts about two places, not one ambiguity.
			name:   "two spans become two labels sharing the note",
			prompt: `$JARGON "load-bearing" and "at its core" — both of these`,
			wantLabels: []monitor.ReportLabel{
				{Token: "JARGON", Span: "load-bearing", Note: "and — both of these"},
				{Token: "JARGON", Span: "at its core", Note: "and — both of these"},
			},
		},
		{
			// Curly quotes: the user is typing prose, and a phone, a browser and
			// a terminal disagree about which quote character they produce.
			name:   "curly quotes scope a span too",
			prompt: "$BLOAT “the whole second paragraph”",
			wantLabels: []monitor.ReportLabel{
				{Token: "BLOAT", Span: "the whole second paragraph"},
			},
		},
		{
			// The refusal E-1953 shipped is GONE. Under a fixed vocabulary a bare
			// `$CUT` carried nothing the user chose; under a free one the token is
			// the account, and refusing it spends the currency the loop is short
			// of.
			name:       "a bare token is now recorded, not refused",
			prompt:     "$GOOD",
			wantLabels: []monitor.ReportLabel{{Token: "GOOD"}},
		},
		{
			name:       "a bare complaint is recorded too",
			prompt:     "$CUT",
			wantLabels: []monitor.ReportLabel{{Token: "CUT"}},
		},
		{
			// Not the first line — the rule is first token of A line, not of THE
			// prompt, so a label after a paragraph of context still counts.
			name:       "label on a later line",
			prompt:     "here's what I meant\n\n$CUT the repro steps are gone",
			wantLabels: []monitor.ReportLabel{{Token: "CUT", Note: "the repro steps are gone"}},
		},
		{
			// Two DIFFERENT tokens are both kept. E-1953 took only the first,
			// because with no span there was no way to tell which reply the second
			// judged; a scoped vocabulary removes the ambiguity that rule existed
			// to dodge.
			name:   "two different tokens are both recorded",
			prompt: "$CUT the verify command\n$BLOAT and it was too long",
			wantLabels: []monitor.ReportLabel{
				{Token: "CUT", Note: "the verify command"},
				{Token: "BLOAT", Note: "and it was too long"},
			},
		},
		{name: "full alone", prompt: "$FULL", wantFull: true},
		{
			// $FULL is a directive, so unlike a label it may carry the actual
			// question with it.
			name:     "full carrying the question",
			prompt:   "$FULL why did the rebase conflict?",
			wantFull: true,
		},
		{
			name:       "full alongside a label",
			prompt:     "$CUT you dropped the verify command\n$FULL",
			wantLabels: []monitor.ReportLabel{{Token: "CUT", Note: "you dropped the verify command"}},
			wantFull:   true,
		},
		{
			name:       "lower case is accepted",
			prompt:     "$cut the table is gone",
			wantLabels: []monitor.ReportLabel{{Token: "CUT", Note: "the table is gone"}},
		},
		{
			name:       "leading indentation is tolerated",
			prompt:     "   $BLOAT too long again",
			wantLabels: []monitor.ReportLabel{{Token: "BLOAT", Note: "too long again"}},
		},

		// --- directives -----------------------------------------------------
		{
			name:       "a pick is a pick and a label at once",
			prompt:     `$B "this sentence"`,
			wantPick:   "B",
			wantLabels: []monitor.ReportLabel{{Token: "B", Span: "this sentence"}},
		},
		{
			name:       "a bare pick",
			prompt:     "$A",
			wantPick:   "A",
			wantLabels: []monitor.ReportLabel{{Token: "A"}},
		},
		{name: "more raises the sample rate", prompt: "$MORE", wantRate: 1},
		{name: "less lowers it", prompt: "$LESS", wantRate: -1},

		// --- inert: the sigil is what buys immunity -------------------------
		{
			// The exact false positive the sigil exists to prevent. `CUT the
			// scope` is an instruction, not a complaint about the last reply.
			name:   "bare word CUT is an instruction",
			prompt: "CUT the scope down to the parser",
		},
		{
			// `WRONG:` and `GOOD:` at line start are what a user naturally types
			// as a prose label, which is precisely why a bare word is unusable.
			name:   "prose label WRONG:",
			prompt: "WRONG: I meant the other file",
		},
		{name: "prose label GOOD:", prompt: "GOOD: that worked"},
		{
			name:   "mid-sentence mention does not fire",
			prompt: "that was $GOOD but the table is gone",
		},
		{
			// The hazard an OPEN vocabulary adds, and the one that would sink it:
			// a pasted shell line beginning with a variable is a quotation, not a
			// label. A fence takes the whole block out of scope.
			name:   "a fenced shell snippet is not a label",
			prompt: "run this:\n```sh\n$PATH=/usr/bin\n$HOME/bin/thing\n```\nthanks",
		},
		{
			// Outside a fence, `$PATH=` still must not fire: the token has to be
			// followed by whitespace or end of line, and `=` is neither.
			name:   "an assignment is not a label",
			prompt: "$PATH=/usr/bin",
		},
		{name: "a positional is not a label", prompt: "$1 is the first arg"},
		{name: "no sigils at all", prompt: "please rerun the tests"},
		{name: "empty prompt", prompt: ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := scanSigils(c.prompt)
			if !labelsEqual(got.Labels, c.wantLabels) {
				t.Errorf("Labels = %+v, want %+v", got.Labels, c.wantLabels)
			}
			if got.Full != c.wantFull {
				t.Errorf("Full = %v, want %v", got.Full, c.wantFull)
			}
			if got.Pick != c.wantPick {
				t.Errorf("Pick = %q, want %q", got.Pick, c.wantPick)
			}
			if got.RateDir != c.wantRate {
				t.Errorf("RateDir = %d, want %d", got.RateDir, c.wantRate)
			}
		})
	}
}

func labelsEqual(got, want []monitor.ReportLabel) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestDriftPair_AsksRatherThanMerges pins the half of ED-1555 that keeps an
// open vocabulary usable. Auto-merging would silently rewrite what the user
// said, and the pair it would get wrong ("$CUT" vs "$CUTS") is exactly the pair
// that is closest.
//
// E-2159 moved WHERE the question goes — the errors channel, not the agent's
// context, since only the user can answer it and nothing is blocked either way
// — so these assert the pair the matcher found rather than a message. The
// matching rules are unchanged, which is what this still guards.
func TestDriftPair_AsksRatherThanMerges(t *testing.T) {
	known := []string{"BLOAT", "GOOD"}

	fresh, near, found := driftPair([]monitor.ReportLabel{{Token: "BLOATED"}}, known)
	if !found {
		t.Fatal("a near-collision raised no question")
	}
	if fresh != "BLOATED" || near != "BLOAT" {
		t.Errorf("pair = ($%s, $%s), want ($BLOATED, $BLOAT)", fresh, near)
	}

	// A token already in use is not "near" — it IS the token, and there is
	// nothing to ask.
	if _, _, got := driftPair([]monitor.ReportLabel{{Token: "BLOAT"}}, known); got {
		t.Error("an established token raised a merge question")
	}
	// Nothing close enough: a distinct word is the user adding vocabulary, which
	// is the behavior the design wants, not a mistake to query.
	if _, _, got := driftPair([]monitor.ReportLabel{{Token: "JARGON"}}, known); got {
		t.Error("an unrelated token raised a merge question")
	}
	// The very first label of all has nothing to be near.
	if _, _, got := driftPair([]monitor.ReportLabel{{Token: "CUT"}}, nil); got {
		t.Error("the first token ever raised a merge question")
	}
}

// TestDriftPair_AtMostOne pins the noise bound. A prompt introducing three new
// words is a user in flow; three incidents auditing their vocabulary is how a
// useful signal becomes a list nobody reads.
func TestDriftPair_AtMostOne(t *testing.T) {
	known := []string{"BLOAT", "CUT"}
	fresh, _, found := driftPair(
		[]monitor.ReportLabel{{Token: "BLOATED"}, {Token: "CUTS"}}, known)
	if !found {
		t.Fatal("two near-collisions raised none")
	}
	if fresh != "BLOATED" {
		t.Errorf("reported $%s; the FIRST fresh token with a neighbour wins", fresh)
	}
}

// A pick is not vocabulary. `$A` and `$B` are the loop's own words, so they must
// never be offered up as tokens the user might want to merge.
func TestDriftPair_IgnoresPicks(t *testing.T) {
	if _, _, got := driftPair([]monitor.ReportLabel{{Token: "B"}}, []string{"A"}); got {
		t.Error("a pick raised a merge question")
	}
}

// TestDriftNoticeStaysOutOfTheTurn is the E-2159 half: vocabulary drift costs
// the agent nothing. A question only the user can answer, about their own
// labels, with nothing blocked either way, has no business in the context.
func TestDriftNoticeStaysOutOfTheTurn(t *testing.T) {
	// recordDrift's own return says it FOUND one; what matters here is that
	// nothing it does produces text for the turn. The fault store is unbound in
	// tests, so Record is a no-op and this exercises the call path only.
	if !recordDrift([]monitor.ReportLabel{{Token: "BLOATED"}}, []string{"BLOAT"}) {
		t.Fatal("recordDrift did not see the collision driftPair sees")
	}
}
