package hookcmd

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// sigils.go holds the `$`-prefixed user vocabulary the minimizer reads from a
// prompt. The parse (everything above the divider) is pure — no DB, no I/O —
// because it is the part that has to be exactly right and it should be provable
// without infrastructure. The functions below the divider apply the parse to the
// session.
//
// E-1953 shipped a CLOSED vocabulary: `$CUT` / `$BLOAT` / `$WRONG` / `$GOOD`
// plus the `$FULL` directive. ED-1555 opens it. The user writes whatever word
// occurs to them, optionally scoped to a quoted span:
//
//	$BLOAT "the whole second paragraph"
//	$JARGON "load-bearing" and "reframes the decision"
//	$GOOD
//
// Why open it. A closed vocabulary is only worth its consistency if the user can
// recall it MID-COMPLAINT, and one they cannot recall produces no label at all —
// which is strictly worse, because label supply is upstream of everything else
// in the loop. So consistency is leaned on rather than enforced: when a new token
// clusters near one already in use, the loop asks in band whether to merge, and
// the user may answer or ignore. Drift becomes observable instead of silent, and
// synonym grouping is the judge's job.
//
// Two classes still share the sigil:
//
//   - LABELS — any token — annotate the PRECEDING turn's corpus row. They are how
//     a persisted turn acquires the one thing it cannot derive: whether the
//     minimization was any good.
//   - DIRECTIVES — `$FULL`, `$A`, `$B`, `$MORE`, `$LESS` — act on the loop rather
//     than describing a reply.
//
// Why a sigil at all. `CUT:` at line start is nearly safe as a bare word, but
// `WRONG:` and `GOOD:` are exactly what a user naturally types as a prose label
// — "WRONG: I meant the other file" is a sentence, not a signal. One character
// buys immunity. `!cut` and `/cut` are unusable: Claude Code already claims both
// prefixes.
//
// Approval is not politeness. A corpus made only of complaints trains the
// minimizer toward verbosity, because every recorded failure is a cut the user
// resented and no recorded success balances it.

// sigilRe matches a token as the FIRST THING ON A LINE, capturing the word and
// whatever follows it on that line.
//
// Line-leading is the whole disambiguation rule, and it is what keeps an OPEN
// vocabulary safe: `CUT the scope` and a mid-sentence "that was $GOOD" stay
// inert while `$CUT you dropped the verify command` fires. The lookahead for
// whitespace-or-end stops `$PATH=x` and `$1` from being read as labels.
//
// Case-insensitive because the vocabulary is typed by a human mid-complaint, and
// bouncing `$cut` for casing would teach the user the signal is unreliable. The
// captured word is upper-cased before use so the corpus stores one spelling.
var sigilRe = regexp.MustCompile(`(?im)^[ \t]*\$([a-z][a-z0-9_]{0,23})(?:$|[ \t]+)(.*)$`)

// spanRe pulls the quoted spans out of a label's trailing text. Straight and
// curly double quotes both count — the user is typing prose, and a terminal, a
// browser and a phone disagree about which quote they produce.
//
// Backticks are deliberately NOT a span delimiter: they are how this project's
// prose writes inline code, so treating them as spans would turn every mention
// of `just test` into a label scope.
var spanRe = regexp.MustCompile("\"([^\"]+)\"|“([^”]+)”")

// fenceRe matches a markdown code-fence line. Lines inside a fence are skipped
// entirely: a user pasting a shell script whose line begins `$PATH` is quoting,
// not labelling, and the same hazard the denylist records for banned phrases
// applies here — a signal that fires on a legitimate quotation is a signal the
// user learns to stop trusting.
var fenceRe = regexp.MustCompile("^[ \t]*`{3,}")

// Directive tokens. They are not labels; they act on the loop.
const (
	directiveFull = "FULL"
	directiveA    = "A"
	directiveB    = "B"
	directiveMore = "MORE"
	directiveLess = "LESS"
)

// sigilScan is the result of reading one user prompt.
type sigilScan struct {
	// Labels are the span-scoped annotations for the preceding turn's row, in
	// the order they were written. Unlike E-1953 this is a LIST: two spans in
	// one prompt are two facts about two places, not an ambiguity to resolve by
	// guessing which the user meant.
	Labels []monitor.ReportLabel
	// Pick is "A" or "B" when the user chose between a paired minimization.
	Pick string
	// RateDir is +1 for `$MORE`, -1 for `$LESS`, 0 when neither appeared.
	RateDir int
	// Full is true when `$FULL` appeared, licensing this one turn to bypass the
	// gate.
	Full bool
}

func (s sigilScan) empty() bool {
	return len(s.Labels) == 0 && s.Pick == "" && s.RateDir == 0 && !s.Full
}

// scanSigils reads a user prompt and returns what it asks for.
//
// Nothing is refused. E-1953 rejected a bare `$CUT` on the grounds that "it was
// bad" with no account of HOW is a sample the minimizer cannot act on — true of
// a FIXED vocabulary, where the token carried no information the user chose.
// Under a free one the token IS the account: the user picked that word over
// every other, and a bare `$JARGON` says something a bare `$CUT` never did.
// Refusing it would spend the one currency the loop is short of.
func scanSigils(prompt string) sigilScan {
	var out sigilScan
	inFence := false

	for _, line := range strings.Split(strings.ReplaceAll(prompt, "\r\n", "\n"), "\n") {
		if fenceRe.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := sigilRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		word := strings.ToUpper(m[1])
		rest := strings.TrimSpace(m[2])

		switch word {
		case directiveFull:
			// A directive, not a label. It may stand alone OR carry the question
			// with it (`$FULL why did the rebase conflict?`), so the trailing
			// text is the user's actual prompt and is deliberately not consumed.
			out.Full = true
			continue
		case directiveMore:
			out.RateDir = 1
			continue
		case directiveLess:
			out.RateDir = -1
			continue
		case directiveA, directiveB:
			// A pick, and possibly a label at the same time. `$B "this sentence"`
			// means "B wins, and that span is still bloat" — one line that both
			// decides the comparison and criticises the winner, which is more
			// than either half alone would say.
			if out.Pick == "" {
				out.Pick = word
			}
		}

		out.Labels = append(out.Labels, labelsFrom(word, rest)...)
	}
	return out
}

// labelsFrom expands one `$TOKEN <rest>` into its label rows: one per quoted
// span, or a single unscoped row when no span was given.
//
// The note on a scoped label is the text with the spans removed, so `$JARGON
// "load-bearing" and "at its core" — both of these` records the same note
// against both spans rather than attributing the connective prose to one of them.
func labelsFrom(token, rest string) (labels []monitor.ReportLabel) {
	matches := spanRe.FindAllStringSubmatch(rest, -1)
	if len(matches) == 0 {
		return []monitor.ReportLabel{{Token: token, Note: rest}}
	}
	note := strings.TrimSpace(spanRe.ReplaceAllString(rest, " "))
	note = strings.Join(strings.Fields(note), " ")
	for _, m := range matches {
		span := m[1]
		if span == "" {
			span = m[2]
		}
		labels = append(labels, monitor.ReportLabel{Token: token, Span: span, Note: note})
	}
	return labels
}

// --- vocabulary drift --------------------------------------------------------

// mergeQuestion asks, in band, whether a newly-seen token means the same thing
// as one the user already uses.
//
// It ASKS rather than merges. Auto-merging two tokens would silently rewrite
// what the user said, and the case it would get wrong — `$CUT` (you cut too
// much) versus `$CUTS` (cut more) — is exactly the case where the two words are
// closest. The user may answer or ignore; either way the drift is now visible,
// which is the whole gain over enforcing a vocabulary nobody can recall.
func mergeQuestion(fresh, near string) string {
	return fmt.Sprintf(
		"Endless: `$%s` is new, and the corpus already has `$%s`. Ask the user "+
			"whether they mean the same thing — if so they can keep using either, "+
			"but say which one they intend as the canonical spelling. Then carry on "+
			"with the turn.",
		fresh, near,
	)
}

// nearestToken returns the known token closest to fresh within an edit distance
// of 2, or "" when none is close enough.
//
// Two, not three: at three, four-letter words start matching each other and the
// question fires constantly, which trains the user to ignore it. An exact match
// is not "near" — it is the same token, and there is nothing to ask.
func nearestToken(fresh string, known []string) string {
	best, bestDist := "", 3
	for _, k := range known {
		if k == fresh {
			return ""
		}
		d := editDistance(fresh, k)
		if d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

// editDistance is Levenshtein over two short ASCII tokens.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// --- applying the scan to the session ---------------------------------------

// applySigils reads the submitted prompt and acts on what it finds: labels the
// preceding turn's corpus row, records an A/B pick, moves the sample rate,
// grants a `$FULL` license for the turn now starting, and returns a notice to
// inject (otherwise "").
//
// Best-effort throughout. Every failure logs and moves on, because none of this
// is worth failing a user's prompt over: a lost label costs one corpus sample,
// and a lost exemption costs one bounce. Refusing to accept the prompt would
// cost the user their turn.
func applySigils(payload claudePayload) string {
	scan := scanSigils(payload.Prompt)
	if scan.empty() {
		return ""
	}

	session, err := monitor.GetActiveSession(payload.SessionID)
	if err != nil || session == nil {
		// Nothing to hang a label or a license on.
		return ""
	}

	if scan.Full {
		if err := monitor.GrantReportExemption(session.ID); err != nil {
			log.Printf("granting $FULL exemption: %v", err)
		}
	}
	if scan.RateDir != 0 {
		if rate, err := monitor.NudgeABRate(scan.RateDir); err != nil {
			log.Printf("nudging A/B sample rate: %v", err)
		} else {
			log.Printf("A/B sample rate now %.2f", rate)
		}
	}
	if scan.Pick != "" {
		if found, err := monitor.RecordPick(session.ID, scan.Pick); err != nil {
			log.Printf("recording A/B pick: %v", err)
		} else if !found {
			log.Printf("$%s: the preceding turn was not a pair", scan.Pick)
		}
	}

	notice := ""
	if len(scan.Labels) > 0 {
		known, err := monitor.KnownLabelTokens()
		if err != nil {
			log.Printf("reading known label tokens: %v", err)
		}
		notice = driftNotice(scan.Labels, known)
		if found, err := monitor.RecordReportLabels(session.ID, scan.Labels); err != nil {
			log.Printf("recording report labels: %v", err)
		} else if !found {
			log.Printf("no prior report to label for session %d", session.ID)
		}
	}
	return notice
}

// driftNotice returns the merge question for the FIRST fresh token that has a
// near neighbour, or "".
//
// First and only one. A prompt introducing three new words is a user in flow,
// and interrupting them three times to audit their vocabulary is how a helpful
// question becomes noise the agent learns to suppress.
func driftNotice(labels []monitor.ReportLabel, known []string) string {
	if len(known) == 0 {
		return ""
	}
	for _, l := range labels {
		switch l.Token {
		case directiveA, directiveB:
			continue
		}
		if near := nearestToken(l.Token, known); near != "" {
			return mergeQuestion(l.Token, near)
		}
	}
	return ""
}

// stageReportTurn records the prompting message and resets the per-turn gate
// state. Runs on every UserPromptSubmit — the prompt is one leg of the corpus
// row and `task report` cannot see it, since the command runs as a subprocess
// with no view of the conversation.
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
