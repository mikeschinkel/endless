package hookcmd

import (
	"strings"
	"testing"
)

// TestScanSigils_Recognized pins the vocabulary. Each case is a prompt a user
// would plausibly type, and the expectation is what the corpus should learn from
// it.
func TestScanSigils_Recognized(t *testing.T) {
	cases := []struct {
		name      string
		prompt    string
		wantLabel string
		wantText  string
		wantFull  bool
		wantRej   string
	}{
		{
			name:      "cut with text",
			prompt:    "$CUT you dropped the verify command",
			wantLabel: labelCut,
			wantText:  "you dropped the verify command",
		},
		{
			name:      "bloat with a quoted phrase",
			prompt:    `$BLOAT still saying "load-bearing"`,
			wantLabel: labelBloat,
			wantText:  `still saying "load-bearing"`,
		},
		{
			name:      "wrong with text",
			prompt:    "$WRONG I asked about the schema, not the hook",
			wantLabel: labelWrong,
			wantText:  "I asked about the schema, not the hook",
		},
		{
			// The one label that may stand alone. A corpus made only of
			// complaints trains the minimizer toward verbosity, because every
			// recorded failure is a cut the user resented — so approval has to
			// be as cheap to give as disapproval.
			name:      "good alone is accepted",
			prompt:    "$GOOD",
			wantLabel: labelGood,
		},
		{
			name:      "good with text",
			prompt:    "$GOOD that's exactly the length I wanted",
			wantLabel: labelGood,
			wantText:  "that's exactly the length I wanted",
		},
		{
			// Not the first line — the rule is first token of A line, not of
			// THE prompt, so a label after a paragraph of context still counts.
			name:      "label on a later line",
			prompt:    "here's what I meant\n\n$CUT the repro steps are gone",
			wantLabel: labelCut,
			wantText:  "the repro steps are gone",
		},
		{
			name:     "full alone",
			prompt:   "$FULL",
			wantFull: true,
		},
		{
			// $FULL is a directive, not a label, so unlike the three complaints
			// it may carry the actual question with it.
			name:     "full carrying the question",
			prompt:   "$FULL why did the rebase conflict?",
			wantFull: true,
		},
		{
			name:      "full alongside a label",
			prompt:    "$CUT you dropped the verify command\n$FULL",
			wantLabel: labelCut,
			wantText:  "you dropped the verify command",
			wantFull:  true,
		},
		{
			name:      "lower case is accepted",
			prompt:    "$cut the table is gone",
			wantLabel: labelCut,
			wantText:  "the table is gone",
		},
		{
			name:      "leading indentation is tolerated",
			prompt:    "   $BLOAT too long again",
			wantLabel: labelBloat,
			wantText:  "too long again",
		},

		// --- refused: a complaint with nothing to learn from ---------------
		{name: "bare cut", prompt: "$CUT", wantRej: "$CUT"},
		{name: "bare bloat", prompt: "$BLOAT", wantRej: "$BLOAT"},
		{name: "bare wrong", prompt: "$WRONG   ", wantRej: "$WRONG"},

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
		{name: "longer word does not fire", prompt: "$CUTTING it down now"},
		{name: "no sigils at all", prompt: "please rerun the tests"},
		{name: "empty prompt", prompt: ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := scanSigils(c.prompt)
			if got.Label != c.wantLabel {
				t.Errorf("Label = %q, want %q", got.Label, c.wantLabel)
			}
			if got.LabelText != c.wantText {
				t.Errorf("LabelText = %q, want %q", got.LabelText, c.wantText)
			}
			if got.Full != c.wantFull {
				t.Errorf("Full = %v, want %v", got.Full, c.wantFull)
			}
			if got.Rejected != c.wantRej {
				t.Errorf("Rejected = %q, want %q", got.Rejected, c.wantRej)
			}
		})
	}
}

// TestScanSigils_FirstLabelWins pins the ambiguity rule. Two different labels in
// one prompt is not a richer sample, it is an unusable one — guessing which the
// user meant would put noise in the corpus under the guise of signal.
func TestScanSigils_FirstLabelWins(t *testing.T) {
	got := scanSigils("$CUT the verify command\n$BLOAT and it was too long")
	if got.Label != labelCut {
		t.Errorf("Label = %q, want the first label (%q)", got.Label, labelCut)
	}
	if got.LabelText != "the verify command" {
		t.Errorf("LabelText = %q, want the first label's text", got.LabelText)
	}
}

// TestScanSigils_BareComplaintDoesNotShadowALaterOne is the case the
// first-label-wins rule could get wrong. A refused `$CUT` must not swallow a
// well-formed complaint further down: nothing was recorded for the first, so
// there is no ambiguity to protect against — but the implementation short
// circuits on Rejected too, so this pins the CHOSEN behavior rather than an
// accident.
//
// The choice is to keep the refusal. A user who typed a bare `$CUT` and then a
// full one is most likely correcting themselves mid-thought, and telling them
// the first was ignored is more useful than silently recording the second under
// a rule they cannot see.
func TestScanSigils_BareComplaintDoesNotShadowALaterOne(t *testing.T) {
	got := scanSigils("$CUT\n$BLOAT far too long")
	if got.Rejected != "$CUT" {
		t.Errorf("Rejected = %q, want the bare complaint to be reported", got.Rejected)
	}
	if got.Label != "" {
		t.Errorf("Label = %q, want none — the refusal is what the user is told about", got.Label)
	}
}

// TestSigilRejectionNotice_SaysNothingWasRecorded pins the honesty requirement.
// A signal silently dropped is worse than one refused out loud: the user
// believes the corpus is learning from them when it is not.
func TestSigilRejectionNotice_SaysNothingWasRecorded(t *testing.T) {
	notice := sigilRejectionNotice("$CUT")
	for _, want := range []string{"$CUT", "not recorded", "needs text"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice missing %q: %s", want, notice)
		}
	}
}
