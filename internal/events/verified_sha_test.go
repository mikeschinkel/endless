// White-box tests for tasks.verified_sha (E-2262): the commit a user's passing
// verify ran at, written on the move to `unlanded` and cleared by every other
// status change — identically by the live executor and the ledger replay.
package events

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/mikeschinkel/endless/internal/taskstatus"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

const passedSHA = "0123456789abcdef0123456789abcdef01234567"

func statusChanged(t *testing.T, taskID int64, from, to, sha string) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskStatusChangedPayload{
		OldStatus: from, NewStatus: to, VerifiedSHA: sha,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       Version,
		TS:      "5WYM00000003",
		Kind:    KindTaskStatusChanged,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: taskIDString(taskID)},
		Actor:   Actor{Kind: ActorCLI, ID: "mike"},
		Payload: payload,
	}
}

func verifiedSHAOf(t *testing.T, db *sql.DB, id int64) sql.NullString {
	t.Helper()
	var sha sql.NullString
	if err := db.QueryRow("SELECT verified_sha FROM tasks WHERE id = ?", id).Scan(&sha); err != nil {
		t.Fatalf("read verified_sha of %d: %v", id, err)
	}
	return sha
}

// appliers runs one event through the live executor on one database and the
// ledger replay on another, so every case below asserts the two agree.
type applier struct {
	name  string
	apply func(*testing.T, *sql.DB, *Event)
}

var appliers = []applier{
	{"live", func(t *testing.T, db *sql.DB, evt *Event) {
		t.Helper()
		var err error
		switch evt.Kind {
		case KindTaskStatusChanged:
			_, err = execTaskStatusChanged(db, evt, nil)
		case KindTaskFieldsUpdated:
			_, err = execTaskFieldsUpdated(db, evt, nil)
		}
		if err != nil {
			t.Fatalf("execute %s: %v", evt.Kind, err)
		}
	}},
	{"replay", func(t *testing.T, db *sql.DB, evt *Event) {
		t.Helper()
		var err error
		switch evt.Kind {
		case KindTaskStatusChanged:
			err = replayTaskStatusChanged(db, evt, &ProjectResult{})
		case KindTaskFieldsUpdated:
			err = replayTaskFieldsUpdated(db, evt, &ProjectResult{})
		}
		if err != nil {
			t.Fatalf("replay %s: %v", evt.Kind, err)
		}
	}},
}

func TestUnlandedRecordsThePassedCommit(t *testing.T) {
	for _, a := range appliers {
		t.Run(a.name, func(t *testing.T) {
			db := newDerivationDB(t)
			seedTask(t, db, 10, nil, int(tasktype.TaskTypeTask), taskstatus.Unverified)

			a.apply(t, db, statusChanged(t, 10, taskstatus.Unverified, taskstatus.Unlanded, passedSHA))
			if got := verifiedSHAOf(t, db, 10); got.String != passedSHA {
				t.Errorf("verified_sha = %v, want %s", got, passedSHA)
			}
		})
	}
}

func TestLeavingUnlandedClearsThePassedCommit(t *testing.T) {
	for _, a := range appliers {
		t.Run(a.name, func(t *testing.T) {
			db := newDerivationDB(t)
			seedTask(t, db, 11, nil, int(tasktype.TaskTypeTask), taskstatus.Unverified)
			a.apply(t, db, statusChanged(t, 11, taskstatus.Unverified, taskstatus.Unlanded, passedSHA))

			a.apply(t, db, statusChanged(t, 11, taskstatus.Unlanded, taskstatus.Unverified, ""))
			if got := verifiedSHAOf(t, db, 11); got.Valid {
				t.Errorf("verified_sha = %q after the reset, want NULL", got.String)
			}
		})
	}
}

// TestFieldsUpdatedClearsExceptOnUnlanded pins clearsVerifiedSHA on the
// `task update --status` path: a status other than `unlanded` clears the pass,
// and a --keep-status self-write on an `unlanded` task keeps it.
func TestFieldsUpdatedClearsExceptOnUnlanded(t *testing.T) {
	person := Actor{Kind: ActorCLI, ID: "mike"}
	for _, a := range appliers {
		t.Run(a.name, func(t *testing.T) {
			db := newDerivationDB(t)
			seedTask(t, db, 12, nil, int(tasktype.TaskTypeTask), taskstatus.Unverified)
			a.apply(t, db, statusChanged(t, 12, taskstatus.Unverified, taskstatus.Unlanded, passedSHA))

			a.apply(t, db, fieldsUpdate(t, 12, map[string]any{"status": taskstatus.Unlanded}, person))
			if got := verifiedSHAOf(t, db, 12); got.String != passedSHA {
				t.Errorf("a keep-status self-write cleared verified_sha (now %v)", got)
			}

			a.apply(t, db, fieldsUpdate(t, 12, map[string]any{"status": taskstatus.Revisit}, person))
			if got := verifiedSHAOf(t, db, 12); got.Valid {
				t.Errorf("verified_sha = %q after a move to revisit, want NULL", got.String)
			}
		})
	}
}
