package events

import (
	"strings"
	"testing"
)

// A voiding's commit names the voiding (E-2578), as a correction's names the
// correction: it leads the subject although the lines it voids precede it, its
// value is the error that stopped the apply, and the commit is never folded.
func TestLedgerCommitMessage_Voiding(t *testing.T) {
	landed := ledgerEvent(KindTaskLanded, EntityTask, "999", `{"merge_commit_sha":"abc1234567"}`)
	voided := ledgerEvent(KindLedgerVoided, EntityTask, "999",
		`{"segment":"db-entries-a7f3-000029.jsonl","refs":[25501],"error":"events: commit: database is locked (5)","reason":"the database COMMIT failed"}`)
	got := LedgerCommitMessage([]Event{landed, voided})
	want := "Endless: ledger.voided E-999 events: commit: databa… +1 more\n\n" +
		"task.landed E-999 abc123456\n" +
		"ledger.voided E-999 events: commit: database is locked (5)"
	if got != want {
		t.Errorf("message =\n%s\nwant\n%s", got, want)
	}
	if !IsLedgerSubject(strings.SplitN(got, "\n", 2)[0]) {
		t.Errorf("a voiding subject is still a ledger subject")
	}
	if !HoldsCorrection([]Event{landed, voided}) {
		t.Errorf("a run with a voiding folds; it must not")
	}

	// A hand-written voiding has no error; its reason is the value.
	byHand := ledgerEvent(KindLedgerVoided, EntityTask, "2486",
		`{"segment":"db-entries-a7f3-000029.jsonl","refs":[25501],"reason":"E-2578: never applied"}`)
	if got := LedgerCommitMessage([]Event{byHand}); got != "Endless: ledger.voided E-2486 E-2578: never applied" {
		t.Errorf("by-hand subject = %q", got)
	}
}
