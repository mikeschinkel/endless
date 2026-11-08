package eventcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/mikeschinkel/endless/internal/dbprovenance"
	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
)

// emitQuestionsAsked is the create flow for task.questions_asked (E-2176). It
// has the same shape as the task and decision creates — lock, apply (SQL, then
// the ledger line, then COMMIT), commit the segment — with one difference:
// what is allocated under the lock is written INTO the payload rather than into
// the entity ref. The entity is the task, already known; the series number and
// the question ids are not, and the ledger has to carry them so a replay
// reproduces the rows rather than re-deriving them in a different order.
//
// The caller sends only question texts. A payload that already names a series
// or an id is refused: those numbers belong to the database, and accepting one
// from outside would let a ledger line disagree with the row it created.
func emitQuestionsAsked(clock *kairos.Clock, ts kairos.Timestamp, project,
	entityTypeStr, entityID, actorKindStr, actorID, sessionID, nodeIDStr,
	projectRoot, payloadStr, correlationID string) error {

	if events.EntityType(entityTypeStr) != events.EntityTask {
		return fmt.Errorf("%s: --entity-type must be %q", events.KindTaskQuestionsAsked, events.EntityTask)
	}
	taskID, err := strconv.ParseInt(entityID, 10, 64)
	if err != nil || taskID <= 0 {
		return fmt.Errorf("%s: --entity-id must be a task id, got %q", events.KindTaskQuestionsAsked, entityID)
	}

	var p events.TaskQuestionsAskedPayload
	if err := json.Unmarshal([]byte(payloadStr), &p); err != nil {
		return fmt.Errorf("%s: invalid payload: %w", events.KindTaskQuestionsAsked, err)
	}
	if p.Series != 0 {
		return fmt.Errorf("%s: payload must not carry a series; it is allocated here", events.KindTaskQuestionsAsked)
	}
	for _, q := range p.Questions {
		if q.ID != 0 {
			return fmt.Errorf("%s: payload must not carry question ids; they are allocated here", events.KindTaskQuestionsAsked)
		}
	}

	series, firstID, tx, err := events.PreAllocateQuestions(taskID, len(p.Questions))
	if err != nil {
		return err
	}
	p.Series = series
	ids := make([]string, len(p.Questions))
	for i := range p.Questions {
		p.Questions[i].ID = firstID + int64(i)
		ids[i] = fmt.Sprintf("EQ-%d", p.Questions[i].ID)
	}
	payload, err := json.Marshal(p)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("marshal payload: %w", err)
	}

	evt := events.Event{
		V:       events.Version,
		TS:      ts.String(),
		Kind:    events.KindTaskQuestionsAsked,
		Project: project,
		Entity: events.EntityRef{
			Type: events.EntityTask,
			ID:   entityID,
		},
		Actor: events.EmittingActor(
			events.ActorKind(actorKindStr), actorID, sessionID),
		CorrelationID: correlationID,
		Payload:       json.RawMessage(payload),
	}
	if err := evt.Validate(); err != nil {
		tx.Rollback()
		return err
	}

	writer, err := events.NewWriter(ledgerRoot(projectRoot), nodeIDStr)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("create writer: %w", err)
	}
	// Asking a question changes no task status, so it cannot cascade into an
	// epic derivation; the apply records none.
	applied, err := tx.Apply(events.Emit{Event: &evt, Writer: writer, Clock: clock})
	if err := finishApply(projectRoot, applied, err); err != nil {
		return err
	}

	_ = dbprovenance.Encode(os.Stdout, map[string]any{
		"ts":     ts.String(),
		"kind":   string(events.KindTaskQuestionsAsked),
		"id":     fmt.Sprintf("E-%d", taskID),
		"series": series,
		"ids":    ids,
	})
	return nil
}
