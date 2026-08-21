package monitor

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/gatekind"
)

// minimizer.go holds the write path the hooks need into the autoresearch loop
// (E-1975): the user's free-form labels, their A/B pick, and the sample rate
// they tune by saying so.
//
// Everything the JUDGE and the OPTIMIZER need lives in Python (src/endless/
// minimizer_*.py) because both are model-calling, and E-1486's boundary puts
// model invocation on that side. This file is deliberately the thin half: what a
// hook firing must record synchronously, and nothing else.
//
// Read paths for humans (`session turn`, `minimizer status`) are Python too —
// they query the same tables directly and adding a Go mirror would be a second
// definition of the same question.

// ReportLabel is one span-scoped label the user wrote into an ordinary reply
// (ED-1555): `$TOKEN "quoted span" free text`.
//
// Span is "" for an unscoped token, which is a verdict on the whole reply.
type ReportLabel struct {
	Token string
	Span  string
	Note  string
}

// LatestReportGate returns the id of the session's most recent corpus row — the
// row a label arriving now would annotate.
//
// "Most recent" is the right target because a label arrives on the turn AFTER
// the one it judges: the user reads the reply, then types their complaint as the
// next prompt. By then the row is closed, so this ignores cleared_at.
func LatestReportGate(sessionID int64) (gateID int64, found bool, err error) {
	db, err := DB()
	if err != nil {
		return 0, false, err
	}
	err = db.QueryRow(
		`SELECT id FROM session_gates
		 WHERE session_id=? AND kind_id=? AND sanctioned_text IS NOT NULL
		 ORDER BY id DESC LIMIT 1`,
		sessionID, int(gatekind.GateKindRelay),
	).Scan(&gateID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("latest report gate for session %d: %w", sessionID, err)
	}
	return gateID, true, nil
}

// RecordReportLabels attaches the user's labels to the session's most recent
// corpus row. found is false when there is no row to annotate, which is normal
// on a session's first turn.
//
// The legacy `label` / `label_text` columns are mirrored from the FIRST label so
// that queries written against E-1953's fixed vocabulary keep answering. They
// are a projection now, not the record: a free-form vocabulary does not fit in
// one column pair, and the row-per-span table is what the judge groups synonyms
// over.
func RecordReportLabels(sessionID int64, labels []ReportLabel) (found bool, err error) {
	if len(labels) == 0 {
		return false, nil
	}
	db, err := DB()
	if err != nil {
		return false, err
	}
	gateID, found, err := LatestReportGate(sessionID)
	if err != nil || !found {
		return false, err
	}
	for _, l := range labels {
		if _, err = db.Exec(
			`INSERT INTO report_labels (gate_id, session_id, token, span, note)
			 VALUES (?, ?, ?, ?, ?)`,
			gateID, sessionID, l.Token, nullString(l.Span), nullString(l.Note),
		); err != nil {
			return true, fmt.Errorf("record label %q for session %d: %w", l.Token, sessionID, err)
		}
	}
	first := labels[0]
	legacyText := first.Span
	if legacyText == "" {
		legacyText = first.Note
	}
	if _, err = db.Exec(
		`UPDATE session_gates SET label=?, label_text=? WHERE id=?`,
		strings.ToLower(first.Token), nullString(legacyText), gateID,
	); err != nil {
		return true, fmt.Errorf("mirror legacy label for session %d: %w", sessionID, err)
	}
	return true, nil
}

// KnownLabelTokens returns every token the user has ever used, most-used first.
//
// Used to notice a NEW token that clusters near an old one, so the loop can ask
// in band whether the two should merge. Asking is the whole mechanism —
// consistency is leaned on, never enforced, because a vocabulary the user cannot
// recall in the moment produces no label at all, and label supply is upstream of
// everything else in the loop.
func KnownLabelTokens() (tokens []string, err error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT token, count(*) AS n FROM report_labels
		 GROUP BY token ORDER BY n DESC, token`,
	)
	if err != nil {
		return nil, fmt.Errorf("query label tokens: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var token string
		var n int
		if err = rows.Scan(&token, &n); err != nil {
			return nil, fmt.Errorf("scan label token: %w", err)
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

// RecordPick records the user's `$A` / `$B` against the session's most recent
// A/B pair. found is false when the last turn was not a pair — a `$A` typed at
// an unpaired turn is simply a label, and is recorded as one by the caller.
//
// This is the only real counterfactual in the corpus. An absolute label says a
// reply was bad; only a pick says one prompt beat another on the same draft,
// which is the exact judgment promotion needs.
func RecordPick(sessionID int64, slot string) (found bool, err error) {
	db, err := DB()
	if err != nil {
		return false, err
	}
	gateID, found, err := LatestReportGate(sessionID)
	if err != nil || !found {
		return false, err
	}
	var pairID sql.NullInt64
	if err = db.QueryRow(`SELECT pair_id FROM session_gates WHERE id=?`, gateID).Scan(&pairID); err != nil {
		return false, fmt.Errorf("read pair for gate %d: %w", gateID, err)
	}
	if !pairID.Valid {
		return false, nil
	}
	res, err := db.Exec(
		`UPDATE session_gates SET picked = CASE WHEN pair_slot=? THEN 1 ELSE 0 END
		 WHERE pair_id=?`,
		strings.ToUpper(slot), pairID.Int64,
	)
	if err != nil {
		return false, fmt.Errorf("record pick for session %d: %w", sessionID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("pick rows affected: %w", err)
	}
	return n > 0, nil
}

// --- loop state --------------------------------------------------------------

// StateABRate keys the A/B sample rate in minimizer_state.
const StateABRate = "ab_rate"

// abRateLadder is the set of rates `$MORE` and `$LESS` step through.
//
// A ladder rather than arithmetic on a float, because the user is answering a
// yes/no question ("more of these?") and a ladder makes each answer a visible,
// reversible move. Multiplying by 1.5 forever produces rates nobody chose and
// nobody can undo in the same number of steps.
//
// The top rung is 0.6, not 1.0: pairing EVERY turn stops being an experiment and
// becomes the product, and a user who wants that has told us the minimizer is
// not ready to run unattended.
var abRateLadder = []float64{0, 0.05, 0.10, 0.15, 0.25, 0.40, 0.60}

// DefaultABRate is where a fresh install starts.
const DefaultABRate = 0.15

// ABRate returns the current A/B sample rate.
func ABRate() (rate float64, err error) {
	raw, found, err := MinimizerState(StateABRate)
	if err != nil || !found {
		return DefaultABRate, err
	}
	rate, err = strconv.ParseFloat(raw, 64)
	if err != nil {
		return DefaultABRate, nil
	}
	return rate, nil
}

// NudgeABRate steps the sample rate one rung up (dir > 0) or down (dir < 0) and
// returns the new value.
//
// There is no config knob for this on purpose: the only seat that can judge the
// annoyance-versus-value trade is the one being annoyed, and it makes that
// judgment in the flow of a reply rather than by opening a file.
func NudgeABRate(dir int) (rate float64, err error) {
	current, err := ABRate()
	if err != nil {
		return current, err
	}
	idx := 0
	for i, r := range abRateLadder {
		if r <= current+1e-9 {
			idx = i
		}
	}
	idx += sign(dir)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(abRateLadder) {
		idx = len(abRateLadder) - 1
	}
	rate = abRateLadder[idx]
	return rate, SetMinimizerState(StateABRate, strconv.FormatFloat(rate, 'f', -1, 64))
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	}
	return 0
}

// MinimizerState reads one loop-state value.
func MinimizerState(key string) (value string, found bool, err error) {
	db, err := DB()
	if err != nil {
		return "", false, err
	}
	err = db.QueryRow(`SELECT value FROM minimizer_state WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read minimizer state %q: %w", key, err)
	}
	return value, true, nil
}

// SetMinimizerState writes one loop-state value.
func SetMinimizerState(key, value string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	if _, err = db.Exec(
		`INSERT INTO minimizer_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value,
		   updated_at=strftime('%Y-%m-%dT%H:%M:%S','now')`,
		key, value,
	); err != nil {
		return fmt.Errorf("write minimizer state %q: %w", key, err)
	}
	return nil
}
