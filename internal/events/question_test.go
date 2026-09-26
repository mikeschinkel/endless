package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

const questionTask int64 = 1337 // seeded by newLandingTestDB

func askedEvent(t *testing.T, taskID, series int64, sessionID string, qs map[int64]string) *Event {
	t.Helper()
	p := TaskQuestionsAskedPayload{Series: series}
	for _, id := range slices.Sorted(maps.Keys(qs)) {
		p.Questions = append(p.Questions, AskedQuestion{ID: id, Question: qs[id]})
	}
	payload, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &Event{
		V:       1,
		TS:      kairosTS(time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)),
		Kind:    KindTaskQuestionsAsked,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: strconv.FormatInt(taskID, 10)},
		Actor:   Actor{Kind: ActorCLI, ID: "user@host", SessionID: sessionID},
		Payload: payload,
	}
}

func resolvedEvent(t *testing.T, qid int64, p TaskQuestionResolvedPayload) *Event {
	t.Helper()
	payload, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &Event{
		V:       1,
		TS:      kairosTS(time.Date(2026, 9, 26, 13, 0, 0, 0, time.UTC)),
		Kind:    KindTaskQuestionResolved,
		Project: "test",
		Entity:  EntityRef{Type: EntityTaskQuestion, ID: strconv.FormatInt(qid, 10)},
		Actor:   Actor{Kind: ActorCLI, ID: "user@host"},
		Payload: payload,
	}
}

type questionRow struct {
	ID, TaskID, Series int64
	Question, Status   string
	Answer, AnsweredBy sql.NullString
	AskedBySession     sql.NullInt64
	CreatedAt, Updated string
}

func readQuestions(t *testing.T, db *sql.DB) []questionRow {
	t.Helper()
	rows, err := db.Query(`SELECT id, task_id, series, question, status, answer,
	    answered_by, asked_by_session, created_at, updated_at
	    FROM task_questions ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var out []questionRow
	for rows.Next() {
		var r questionRow
		if err := rows.Scan(&r.ID, &r.TaskID, &r.Series, &r.Question, &r.Status,
			&r.Answer, &r.AnsweredBy, &r.AskedBySession, &r.CreatedAt, &r.Updated); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func TestTaskQuestionsAsked_InsertsOneOpenRowPerQuestion(t *testing.T) {
	db := newLandingTestDB(t)
	evt := askedEvent(t, questionTask, 1, "42", map[int64]string{1: "First?", 2: "Second?"})
	if _, err := execTaskQuestionsAsked(db, evt); err != nil {
		t.Fatalf("exec: %v", err)
	}
	got := readQuestions(t, db)
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	for i, r := range got {
		if r.TaskID != questionTask || r.Series != 1 || r.Status != "open" {
			t.Errorf("row %d = %+v, want task %d series 1 open", i, r, questionTask)
		}
		if !r.AskedBySession.Valid || r.AskedBySession.Int64 != 42 {
			t.Errorf("row %d asked_by_session = %v, want 42", i, r.AskedBySession)
		}
		if r.CreatedAt != "2026-09-26T12:00:00" || r.Updated != r.CreatedAt {
			t.Errorf("row %d timestamps = %q/%q, want the event ts", i, r.CreatedAt, r.Updated)
		}
		if r.Answer.Valid || r.AnsweredBy.Valid {
			t.Errorf("row %d carries an answer at ask time: %+v", i, r)
		}
	}
}

func TestTaskQuestionsAsked_Refusals(t *testing.T) {
	cases := map[string]*Event{}
	db := newLandingTestDB(t)
	cases["unknown task"] = askedEvent(t, 999, 1, "", map[int64]string{1: "q?"})
	cases["zero series"] = askedEvent(t, questionTask, 0, "", map[int64]string{1: "q?"})
	cases["empty question"] = askedEvent(t, questionTask, 1, "", map[int64]string{1: "   "})
	cases["no questions"] = askedEvent(t, questionTask, 1, "", map[int64]string{})
	for name, evt := range cases {
		if _, err := execTaskQuestionsAsked(db, evt); err == nil {
			t.Errorf("%s: accepted, want refusal", name)
		}
	}
	if n := len(readQuestions(t, db)); n != 0 {
		t.Errorf("refusals left %d rows behind", n)
	}
}

func TestTaskQuestionsAsked_RefusesRemovedTask(t *testing.T) {
	db := newLandingTestDB(t)
	if _, err := db.Exec("UPDATE tasks SET removed = 1 WHERE id = ?", questionTask); err != nil {
		t.Fatalf("remove: %v", err)
	}
	evt := askedEvent(t, questionTask, 1, "", map[int64]string{1: "q?"})
	if _, err := execTaskQuestionsAsked(db, evt); err == nil {
		t.Fatal("asked a question of a removed task")
	}
}

func seedOpenQuestions(t *testing.T, db *sql.DB) {
	t.Helper()
	evt := askedEvent(t, questionTask, 1, "", map[int64]string{1: "a?", 2: "b?", 3: "c?"})
	if _, err := execTaskQuestionsAsked(db, evt); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestTaskQuestionResolved_AnswerRecordsWhoAnswered(t *testing.T) {
	db := newLandingTestDB(t)
	seedOpenQuestions(t, db)
	for qid, by := range map[int64]string{1: "user", 2: "ES-1236"} {
		evt := resolvedEvent(t, qid, TaskQuestionResolvedPayload{
			Status: "answered", Answer: "yes", AnsweredBy: by,
		})
		if _, err := execTaskQuestionResolved(db, evt); err != nil {
			t.Fatalf("answer EQ-%d: %v", qid, err)
		}
	}
	got := readQuestions(t, db)
	if got[0].AnsweredBy.String != "user" || got[1].AnsweredBy.String != "ES-1236" {
		t.Errorf("answered_by = %q, %q", got[0].AnsweredBy.String, got[1].AnsweredBy.String)
	}
	if got[0].Status != "answered" || got[0].Answer.String != "yes" {
		t.Errorf("EQ-1 = %+v", got[0])
	}
	if got[0].Updated != "2026-09-26T13:00:00" {
		t.Errorf("updated_at = %q, want the resolving event's ts", got[0].Updated)
	}
	if got[2].Status != "open" {
		t.Errorf("EQ-3 status = %q, want untouched open", got[2].Status)
	}
}

func TestTaskQuestionResolved_SupersedingAnAnswerKeepsIt(t *testing.T) {
	db := newLandingTestDB(t)
	seedOpenQuestions(t, db)
	steps := []TaskQuestionResolvedPayload{
		{Status: "answered", Answer: "60", AnsweredBy: "user"},
		{Status: "superseded"},
	}
	for _, p := range steps {
		if _, err := execTaskQuestionResolved(db, resolvedEvent(t, 1, p)); err != nil {
			t.Fatalf("%s: %v", p.Status, err)
		}
	}
	r := readQuestions(t, db)[0]
	if r.Status != "superseded" || r.Answer.String != "60" || r.AnsweredBy.String != "user" {
		t.Errorf("after supersede = %+v, want the answer kept", r)
	}
}

func TestTaskQuestionResolved_Refusals(t *testing.T) {
	cases := []struct {
		name  string
		setup []TaskQuestionResolvedPayload
		p     TaskQuestionResolvedPayload
		want  string
	}{
		{name: "back to open", p: TaskQuestionResolvedPayload{Status: "open"}, want: "new series"},
		{name: "unknown status", p: TaskQuestionResolvedPayload{Status: "closed"}, want: "unknown"},
		{name: "answer without text", p: TaskQuestionResolvedPayload{Status: "answered", AnsweredBy: "user"}, want: "non-empty answer"},
		{name: "answer without answerer", p: TaskQuestionResolvedPayload{Status: "answered", Answer: "x"}, want: "answered_by"},
		{name: "bad answerer", p: TaskQuestionResolvedPayload{Status: "answered", Answer: "x", AnsweredBy: "1236"}, want: "answered_by"},
		{name: "withdraw with answer", p: TaskQuestionResolvedPayload{Status: "withdrawn", Answer: "x"}, want: "takes no answer"},
		{
			name:  "re-answer",
			setup: []TaskQuestionResolvedPayload{{Status: "answered", Answer: "x", AnsweredBy: "user"}},
			p:     TaskQuestionResolvedPayload{Status: "answered", Answer: "y", AnsweredBy: "user"},
			want:  `from "answered" to "answered"`,
		},
		{
			name:  "withdraw an invalid one",
			setup: []TaskQuestionResolvedPayload{{Status: "invalid"}},
			p:     TaskQuestionResolvedPayload{Status: "withdrawn"},
			want:  `from "invalid" to "withdrawn"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newLandingTestDB(t)
			seedOpenQuestions(t, db)
			for _, s := range tc.setup {
				if _, err := execTaskQuestionResolved(db, resolvedEvent(t, 1, s)); err != nil {
					t.Fatalf("setup: %v", err)
				}
			}
			_, err := execTaskQuestionResolved(db, resolvedEvent(t, 1, tc.p))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestTaskQuestionResolved_UnknownQuestion(t *testing.T) {
	db := newLandingTestDB(t)
	_, err := execTaskQuestionResolved(db, resolvedEvent(t, 77, TaskQuestionResolvedPayload{Status: "withdrawn"}))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want not found", err)
	}
}

// TestTaskQuestions_ReplayMatchesLive runs one event sequence through the live
// executor on one database and the replay path on another, and requires the
// rows to be identical. Both paths call the same apply functions; this pins it,
// so a future edit that forks them fails here rather than on a rebuild.
func TestTaskQuestions_ReplayMatchesLive(t *testing.T) {
	seq := []*Event{
		askedEvent(t, questionTask, 1, "7", map[int64]string{1: "a?", 2: "b?"}),
		resolvedEvent(t, 1, TaskQuestionResolvedPayload{Status: "answered", Answer: "A", AnsweredBy: "ES-9"}),
		resolvedEvent(t, 2, TaskQuestionResolvedPayload{Status: "invalid"}),
		askedEvent(t, questionTask, 2, "", map[int64]string{3: "c?"}),
		resolvedEvent(t, 1, TaskQuestionResolvedPayload{Status: "superseded"}),
	}
	live := newLandingTestDB(t)
	replay := newLandingTestDB(t)
	for i, evt := range seq {
		if _, err := dispatch(live, evt, nil); err != nil {
			t.Fatalf("live step %d: %v", i, err)
		}
		if err := replayEvent(replay, evt, &ProjectResult{}); err != nil {
			t.Fatalf("replay step %d: %v", i, err)
		}
	}
	l, r := readQuestions(t, live), readQuestions(t, replay)
	if !reflect.DeepEqual(l, r) {
		t.Errorf("replay diverged from live:\nlive:   %+v\nreplay: %+v", l, r)
	}
	if len(l) != 3 {
		t.Errorf("rows = %d, want 3", len(l))
	}
}

func TestTaskQuestionEvents_Validate(t *testing.T) {
	for _, evt := range []*Event{
		askedEvent(t, questionTask, 1, "", map[int64]string{1: "a?"}),
		resolvedEvent(t, 1, TaskQuestionResolvedPayload{Status: "withdrawn"}),
	} {
		if err := evt.Validate(); err != nil {
			t.Errorf("%s: %v", evt.Kind, err)
		}
	}
}

func withQuestionExecutorDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO projects (id, name, path) VALUES (1, 'test', '/tmp/test')`,
		`INSERT INTO tasks (id, project_id, title, phase, status, type_id) VALUES (1, 1, 'one', 'now', 'ready', 1)`,
		`INSERT INTO tasks (id, project_id, title, phase, status, type_id) VALUES (2, 1, 'two', 'now', 'ready', 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	t.Cleanup(monitor.SetTestDB(db))
	return db
}

// ask runs PreAllocateQuestions end to end the way `event emit` does: allocate,
// fill the payload, execute and commit.
func ask(t *testing.T, taskID int64, texts ...string) (series, firstID int64) {
	t.Helper()
	series, firstID, execAndCommit, rollback, err := PreAllocateQuestions(taskID, len(texts))
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	qs := map[int64]string{}
	for i, q := range texts {
		qs[firstID+int64(i)] = q
	}
	evt := askedEvent(t, taskID, series, "", qs)
	if _, err := execAndCommit(evt, nil); err != nil {
		rollback()
		t.Fatalf("exec: %v", err)
	}
	return series, firstID
}

func TestPreAllocateQuestions_SeriesPerTaskIdsGlobal(t *testing.T) {
	withQuestionExecutorDB(t)
	cases := []struct {
		task               int64
		n                  int
		wantSeries, wantID int64
	}{
		{1, 2, 1, 1},
		{1, 1, 2, 3},
		{2, 3, 1, 4}, // a new task starts at series 1; ids keep counting
		{1, 1, 3, 7},
	}
	for i, tc := range cases {
		texts := make([]string, tc.n)
		for j := range texts {
			texts[j] = fmt.Sprintf("q%d-%d?", i, j)
		}
		s, id := ask(t, tc.task, texts...)
		if s != tc.wantSeries || id != tc.wantID {
			t.Errorf("step %d: series/first id = %d/%d, want %d/%d", i, s, id, tc.wantSeries, tc.wantID)
		}
	}
}

func TestPreAllocateQuestions_RefusesUnknownTaskAndReleasesLock(t *testing.T) {
	withQuestionExecutorDB(t)
	if _, _, _, _, err := PreAllocateQuestions(99, 1); err == nil {
		t.Fatal("allocated for an unknown task")
	}
	if _, _, _, _, err := PreAllocateQuestions(1, 0); err == nil {
		t.Fatal("allocated zero questions")
	}
	// The refusal must have released the write lock.
	if s, _ := ask(t, 1, "after?"); s != 1 {
		t.Errorf("series after refusal = %d, want 1", s)
	}
}
