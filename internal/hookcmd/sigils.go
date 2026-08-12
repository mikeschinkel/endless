package hookcmd

import (
	"log"
	"regexp"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// sigils.go holds the `$`-prefixed user vocabulary the minimizer reads from a
// prompt (E-1953). The parse (everything above the divider) is pure — no DB, no
// I/O — because it is the part that has to be exactly right and it should be
// provable without infrastructure. The two functions below the divider apply
// the parse to the session.
//
// Two classes share one sigil:
//
//   - LABELS — `$CUT` / `$BLOAT` / `$WRONG` / `$GOOD` — annotate the PRECEDING
//     turn's corpus row. They are how a persisted (prompt, draft, minimized)
//     triple acquires the one thing it cannot derive: whether the minimization
//     was any good.
//   - A DIRECTIVE — `$FULL` — licenses the turn in flight to bypass the gate
//     entirely.
//
// The hook routes on the word, not the class, so both are one scan.
//
// Why a sigil at all. `CUT:` at line start is nearly safe as a bare word, but
// `WRONG:` and `GOOD:` are exactly what a user naturally types as a prose label
// — "WRONG: I meant the other file" is a sentence, not a signal. One character
// buys immunity. `!cut` and `/cut` are unusable: Claude Code already claims both
// prefixes.
//
// `$GOOD` is not politeness. A corpus made only of complaints trains the
// minimizer toward verbosity, because every recorded failure is a cut the user
// resented and no recorded success balances it.

// sigilRe matches a recognized sigil as the FIRST TOKEN OF A LINE, capturing the
// word and whatever follows it on that line.
//
// Line-leading is the whole disambiguation rule. It is what makes `CUT the
// scope` and a mid-sentence "that was $GOOD" inert while `$CUT you dropped the
// verify command` fires. `\b` after the word stops `$CUTTING` from matching.
//
// Case-insensitive because the vocabulary is typed by a human mid-complaint, and
// bouncing `$cut` for casing would teach the user the signal is unreliable. The
// captured word is upper-cased before use so the corpus stores one spelling.
var sigilRe = regexp.MustCompile(`(?im)^[ \t]*\$(CUT|BLOAT|WRONG|GOOD|FULL)\b[ \t]*(.*)$`)

// Label values as persisted. Lower-case because they are data, not display.
const (
	labelCut   = "cut"
	labelBloat = "bloat"
	labelWrong = "wrong"
	labelGood  = "good"
)

// sigilScan is the result of reading one user prompt.
type sigilScan struct {
	// Label is the corpus label to attach to the preceding turn's row, or "".
	Label string
	// LabelText is the free text the user gave with it. Always non-empty when
	// Label is one of cut/bloat/wrong; may be empty for good.
	LabelText string
	// Rejected names a label sigil that appeared with no text where text is
	// required (e.g. "$CUT"). Nothing is recorded for it — a bare complaint
	// gives the corpus nothing to learn from, and silently storing an empty
	// label would pollute the corpus with samples that teach nothing.
	Rejected string
	// Full is true when `$FULL` appeared, licensing this one turn to bypass the
	// gate.
	Full bool
}

// scanSigils reads a user prompt and returns what it asks for.
//
// At most one label is taken — the first in the prompt. A prompt bearing two
// different labels is not a richer sample, it is an ambiguous one, and guessing
// which the user meant would put noise in the corpus under the guise of signal.
// `$FULL` is scanned independently because it is a different class: it can
// legitimately accompany a label, and it can carry the actual question with it
// (`$FULL why did the rebase conflict?`).
func scanSigils(prompt string) sigilScan {
	var out sigilScan
	for _, m := range sigilRe.FindAllStringSubmatch(prompt, -1) {
		word := strings.ToUpper(m[1])
		text := strings.TrimSpace(m[2])

		if word == "FULL" {
			// A directive, not a label. Unlike the three complaints it may stand
			// alone OR carry the question with it, so the trailing text is the
			// user's actual prompt and is deliberately not consumed here.
			out.Full = true
			continue
		}
		if out.Label != "" || out.Rejected != "" {
			continue
		}
		label := map[string]string{
			"CUT": labelCut, "BLOAT": labelBloat, "WRONG": labelWrong, "GOOD": labelGood,
		}[word]
		// `$GOOD` may stand alone; the three complaints may not. "It was bad"
		// with no account of HOW is the one sample the minimizer cannot act on,
		// so it is refused at the door rather than stored and averaged in.
		if text == "" && label != labelGood {
			out.Rejected = "$" + word
			continue
		}
		out.Label, out.LabelText = label, text
	}
	return out
}

// sigilRejectionNotice is injected when a complaint arrives with no text. It is
// addressed at the agent because that is the only channel a UserPromptSubmit
// hook has, but the fix belongs to the user — so it asks rather than explains,
// and says plainly that nothing was recorded. A signal silently dropped is worse
// than one refused out loud: the user believes the corpus is learning from them.
func sigilRejectionNotice(sigil string) string {
	return "Endless: `" + sigil + "` was not recorded — it needs text saying what " +
		"was wrong with the last reply (e.g. `" + sigil + " you dropped the verify " +
		"command`). Ask the user what they meant, then carry on with the turn."
}

// --- applying the scan to the session ---------------------------------------

// applySigils reads the submitted prompt and acts on what it finds: labels the
// preceding turn's corpus row, grants a `$FULL` license for the turn now
// starting, and returns a notice to inject when a complaint arrived with no
// text (otherwise "").
//
// Best-effort throughout. Every failure logs and moves on, because none of this
// is worth failing a user's prompt over: a lost label costs one corpus sample,
// and a lost exemption costs one bounce. Refusing to accept the prompt would
// cost the user their turn.
func applySigils(payload claudePayload) string {
	scan := scanSigils(payload.Prompt)
	if scan.Label == "" && scan.Rejected == "" && !scan.Full {
		return ""
	}

	session, err := monitor.GetActiveSession(payload.SessionID)
	if err != nil || session == nil {
		// Nothing to hang a label or a license on. Still return the rejection
		// notice — it is the user's mistake either way, and telling them is
		// independent of whether the corpus row could be found.
		if scan.Rejected != "" {
			return sigilRejectionNotice(scan.Rejected)
		}
		return ""
	}

	if scan.Full {
		if err := monitor.GrantReportExemption(session.ID); err != nil {
			log.Printf("granting $FULL exemption: %v", err)
		}
	}
	if scan.Label != "" {
		found, err := monitor.LabelLatestReport(session.ID, scan.Label, scan.LabelText)
		if err != nil {
			log.Printf("labeling latest report: %v", err)
		} else if !found {
			log.Printf("$%s: no prior report to label for session %d",
				strings.ToUpper(scan.Label), session.ID)
		}
	}
	if scan.Rejected != "" {
		return sigilRejectionNotice(scan.Rejected)
	}
	return ""
}

// stageReportTurn records the prompting message and resets the per-turn gate
// state. Runs on every UserPromptSubmit — the prompt is one leg of the corpus
// triple and `task report` cannot see it, since the command runs as a
// subprocess with no view of the conversation.
//
// Runs BEFORE applySigils. The reset is what makes the bounce budget and the
// `$FULL` license per-turn rather than per-session, so it has to happen first;
// running it after would wipe the license applySigils had just granted.
func stageReportTurn(payload claudePayload) {
	session, err := monitor.GetActiveSession(payload.SessionID)
	if err != nil || session == nil {
		return
	}
	if err := monitor.StageUserPrompt(session.ID, payload.Prompt); err != nil {
		log.Printf("staging user prompt: %v", err)
	}
}
