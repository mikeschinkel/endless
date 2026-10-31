package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ledgerEvent builds one event the way `event emit` writes it.
func ledgerEvent(kind Kind, typ EntityType, id, payload string) Event {
	return Event{
		V:       Version,
		Kind:    kind,
		Entity:  EntityRef{Type: typ, ID: id},
		Payload: json.RawMessage(payload),
	}
}

// ledgerLine is ledgerEvent marshalled as one JSONL line.
func ledgerLine(t *testing.T, e Event) string {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return string(b)
}

// TestLedgerCommitMessage_OneEvent pins each kind's subject: kind, entity by
// display id, and the value it set, within 60 runes (E-2498).
func TestLedgerCommitMessage_OneEvent(t *testing.T) {
	for _, tc := range []struct {
		name string
		evt  Event
		want string
	}{
		{"status", ledgerEvent(KindTaskStatusChanged, EntityTask, "2421",
			`{"old_status":"underway","new_status":"unverified"}`),
			"Endless: task.status_changed E-2421 → unverified"},
		{"derived", ledgerEvent(KindEpicStatusDerived, EntityTask, "2400",
			`{"task_id":2400,"old_status":"ready","new_status":"underway"}`),
			"Endless: epic.status_derived E-2400 → underway"},
		{"created", ledgerEvent(KindTaskCreated, EntityTask, "2510",
			`{"title":"Fix it"}`),
			"Endless: task.created E-2510 Fix it"},
		{"long title", ledgerEvent(KindTaskCreated, EntityTask, "2510",
			`{"title":"Name the event in each ledger commit's subject, giving its kind"}`),
			"Endless: task.created E-2510 Name the event in each ledger…"},
		{"fields", ledgerEvent(KindTaskFieldsUpdated, EntityTask, "7",
			`{"fields":{"title":"x","phase":"now"}}`),
			"Endless: task.fields_updated E-7 phase, title"},
		{"decision", ledgerEvent(KindDecisionCreated, EntityDecision, "1601",
			`{"title":"Keep folding"}`),
			"Endless: decision.created ED-1601 Keep folding"},
		{"dep", ledgerEvent(KindTaskDepCreated, EntityTaskDep, "2484",
			`{"source_id":2484,"target_id":2481,"dep_type":"cleans_up"}`),
			"Endless: task_dep.created E-2484 cleans_up E-2481"},
		{"relation", ledgerEvent(KindDecisionRelationCreated, EntityDecisionRelation, "0",
			`{"source_decision_id":1625,"target_kind":"task","target_id":2230,"relation_type":"documents"}`),
			"Endless: decision_relation.created ED-1625 documents E-2230"},
		{"question", ledgerEvent(KindTaskQuestionResolved, EntityTaskQuestion, "199",
			`{"status":"answered","answer":"long text"}`),
			"Endless: task_question.resolved EQ-199 → answered"},
		{"plan items", ledgerEvent(KindTaskPlanItemsChanged, EntityTask, "2498",
			`{"ops":[{"op":"add","seq":2},{"op":"withdraw","seq":3}]}`),
			"Endless: task.plan_items_changed E-2498 add P2, withdraw P3"},
		{"landed", ledgerEvent(KindTaskLanded, EntityTask, "2480",
			`{"base_branch":"main","merge_commit_sha":"816f55054dec18920c469247b111502965e03fdc"}`),
			"Endless: task.landed E-2480 816f55054"},
		{"claimed", ledgerEvent(KindTaskClaimed, EntityTask, "2217", `{"session_id":1292}`),
			"Endless: task.claimed E-2217 ES-1292"},
		{"no value", ledgerEvent(KindTaskDeleted, EntityTask, "9", `{}`),
			"Endless: task.deleted E-9"},
		{"long path", ledgerEvent(KindShippedFileWritten, EntityShippedFile,
			".endless/tasks/some/deeply/nested/directory/CLAUDE.md", `{}`),
			"Endless: shipped_file.written …ly/nested/directory/CLAUDE.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := LedgerCommitMessage([]Event{tc.evt})
			if got != tc.want {
				t.Errorf("message = %q\n          want %q", got, tc.want)
			}
			if n := runeLen(got); n > ledgerSubjectLimit {
				t.Errorf("subject is %d runes, over %d", n, ledgerSubjectLimit)
			}
			if !IsLedgerSubject(got) {
				t.Errorf("IsLedgerSubject(%q) = false", got)
			}
		})
	}
}

// TestLedgerCommitMessage_Folded: several events name the first plus a count,
// and the body lists every one of them.
func TestLedgerCommitMessage_Folded(t *testing.T) {
	got := LedgerCommitMessage([]Event{
		ledgerEvent(KindTaskCreated, EntityTask, "2510",
			`{"title":"Fix the ledger subject so it names what it holds"}`),
		ledgerEvent(KindTaskDepCreated, EntityTaskDep, "2510",
			`{"source_id":2510,"target_id":2498,"dep_type":"cleans_up"}`),
		ledgerEvent(KindEpicStatusDerived, EntityTask, "2400", `{"new_status":"underway"}`),
	})
	want := "Endless: task.created E-2510 Fix the ledger subject… +2 more\n\n" +
		"task.created E-2510 Fix the ledger subject so it names what it holds\n" +
		"task_dep.created E-2510 cleans_up E-2498\n" +
		"epic.status_derived E-2400 → underway"
	if got != want {
		t.Errorf("message =\n%s\nwant\n%s", got, want)
	}
	subject, _, _ := strings.Cut(got, "\n")
	if n := runeLen(subject); n > ledgerSubjectLimit {
		t.Errorf("subject is %d runes, over %d", n, ledgerSubjectLimit)
	}
}

// TestLedgerCommitMessage_NoEvents falls back to the legacy subject, which is
// still recognised as a ledger subject.
func TestLedgerCommitMessage_NoEvents(t *testing.T) {
	if got := LedgerCommitMessage(nil); got != LegacyLedgerCommitSubject {
		t.Errorf("message = %q, want %q", got, LegacyLedgerCommitSubject)
	}
}

// TestLedgerCommitMessage_UndottedKind keeps a recognisable subject, so the
// next event still folds in, and still lists the event in the body.
func TestLedgerCommitMessage_UndottedKind(t *testing.T) {
	got := LedgerCommitMessage([]Event{ledgerEvent("odd", EntityTask, "1", `{}`)})
	want := LegacyLedgerCommitSubject + "\n\nodd E-1"
	if got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

func TestIsLedgerSubject(t *testing.T) {
	for subject, want := range map[string]bool{
		LegacyLedgerCommitSubject:                      true,
		"Endless: task.status_changed E-1 → confirmed": true,
		"Endless: task.created E-1 Fix it +2 more":     true,
		"Endless: task.deleted":                        true,
		"Endless: auto-record session activity":        false,
		"Endless: add context for E-2505":              false,
		"Endless: verify E-1 verify-x.ctrf.json":       false,
		"E-2498: name the event":                       false,
		"endless: task.created E-1":                    false,
	} {
		if got := IsLedgerSubject(subject); got != want {
			t.Errorf("IsLedgerSubject(%q) = %v, want %v", subject, got, want)
		}
	}
}

// TestCommitLedgerSegment_SubjectNamesEvents drives the real commit path: the
// first event titles a new commit, the second folds into it by amend and the
// rebuilt message names both.
func TestCommitLedgerSegment_SubjectNamesEvents(t *testing.T) {
	root, segRel := initRepo(t)
	first := ledgerLine(t, ledgerEvent(KindTaskStatusChanged, EntityTask, "2421",
		`{"new_status":"unverified"}`))
	if err := os.WriteFile(filepath.Join(root, segRel), []byte(first+"\n"), 0644); err != nil {
		t.Fatalf("write segment: %v", err)
	}
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if got := mustGit(t, root, "log", "-1", "--format=%B"); got != "Endless: task.status_changed E-2421 → unverified" {
		t.Fatalf("first message = %q", got)
	}
	parent := mustGit(t, root, "rev-parse", "HEAD^")

	appendLine(t, root, segRel, ledgerLine(t, ledgerEvent(KindTaskClaimed, EntityTask, "2498",
		`{"session_id":1394}`)))
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("second commit: %v", err)
	}
	if got := mustGit(t, root, "rev-parse", "HEAD^"); got != parent {
		t.Fatalf("second event should amend, but HEAD's parent moved %s → %s", parent, got)
	}
	want := "Endless: task.status_changed E-2421 → unverified +1 more\n\n" +
		"task.status_changed E-2421 → unverified\n" +
		"task.claimed E-2498 ES-1394"
	if got := mustGit(t, root, "log", "-1", "--format=%B"); got != want {
		t.Fatalf("folded message =\n%s\nwant\n%s", got, want)
	}
}

// TestCommitLedgerSegment_AutoRecordHeadNotAmended: land's auto-record commit
// can hold nothing but ledger lines, yet it is not a ledger commit and a ledger
// write must append rather than rewrite it.
func TestCommitLedgerSegment_AutoRecordHeadNotAmended(t *testing.T) {
	root, segRel := initRepo(t)
	mustGit(t, root, "add", segRel)
	mustGit(t, root, "commit", "-q", "-m", "Endless: auto-record session activity")
	autoRecord := mustGit(t, root, "rev-parse", "HEAD")

	appendLine(t, root, segRel, `{"v":"1","kind":"task.deleted","entity":{"type":"task","id":"5"}}`)
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("ledger commit: %v", err)
	}
	if got := mustGit(t, root, "rev-parse", "HEAD^"); got != autoRecord {
		t.Fatalf("ledger commit should sit on the auto-record commit, parent = %s", got)
	}
	if got := mustGit(t, root, "log", "-1", "--format=%s"); got != "Endless: task.deleted E-5" {
		t.Fatalf("subject = %q", got)
	}
}

// TestIsLedgerCommit: the subject alone is not enough; a commit touching
// anything outside the ledger is not a ledger commit.
func TestIsLedgerCommit(t *testing.T) {
	root, segRel := initRepo(t)
	mustGit(t, root, "add", segRel)
	mustGit(t, root, "commit", "-q", "-m", LegacyLedgerCommitSubject)
	if ok, err := IsLedgerCommit(root, "HEAD", LegacyLedgerCommitSubject); err != nil || !ok {
		t.Fatalf("legacy ledger commit: ok=%v err=%v, want true", ok, err)
	}

	appendLine(t, root, segRel, `{"v":"1"}`)
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("changed\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustGit(t, root, "commit", "-q", "-a", "-m", "Endless: task.deleted E-5")
	if ok, err := IsLedgerCommit(root, "HEAD", "Endless: task.deleted E-5"); err != nil || ok {
		t.Fatalf("commit touching README: ok=%v err=%v, want false", ok, err)
	}
}
