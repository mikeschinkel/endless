package eventcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/dbprovenance"
	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/planitem"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// isPreparedKind reports whether a kind goes through emitPrepared: its payload
// is validated and completed against the current rows under the write lock,
// before the ledger append (E-2280).
func isPreparedKind(k events.Kind) bool {
	switch k {
	case events.KindTaskPlanItemsChanged, events.KindTaskPlanBaselined, events.KindTaskReconciled,
		events.KindTaskPostLandReconciled:
		return true
	}
	return false
}

// emitPrepared is the create flow for the plan item and reconciliation kinds.
// The shape of emitQuestionsAsked — lock, prepare, apply (SQL, then the
// ledger line, then COMMIT), commit the segment — with the preparation chosen
// by kind. A refusal from preparation leaves nothing behind: the lock is
// rolled back before anything reaches the ledger, so a refused change can
// never become a line every replay then fails on.
func emitPrepared(evtKind events.Kind, clock *kairos.Clock, ts kairos.Timestamp, project,
	entityTypeStr, entityID, actorKindStr, actorID, sessionID, nodeIDStr,
	projectRoot, payloadStr, correlationID string) error {

	if events.EntityType(entityTypeStr) != events.EntityTask {
		return refusal.NoReport(
			fmt.Sprintf("%s: --entity-type must be %q", evtKind, events.EntityTask),
			"Retry with --entity-type task")
	}
	taskID, err := strconv.ParseInt(entityID, 10, 64)
	if err != nil || taskID <= 0 {
		return refusal.NoReport(
			fmt.Sprintf("%s: --entity-id must be a task id, got %q", evtKind, entityID),
			"Retry with the task's numeric id")
	}

	tx, err := events.BeginWrite()
	if err != nil {
		return err
	}

	var payload any
	var summary map[string]any
	switch evtKind {
	case events.KindTaskPlanItemsChanged:
		var p events.TaskPlanItemsChangedPayload
		if err = json.Unmarshal([]byte(payloadStr), &p); err == nil {
			err = events.PreparePlanItemsChangedBy(tx.DB(), taskID, &p, events.DetectedHarness() != "")
		}
		var seqs []string
		for _, op := range p.Ops {
			if op.Op == events.PlanOpAdd {
				seqs = append(seqs, planitem.Label(op.Seq))
			}
		}
		payload, summary = p, map[string]any{"added": seqs}
	case events.KindTaskPlanBaselined:
		var p events.TaskPlanBaselinedPayload
		if strings.TrimSpace(payloadStr) != "" {
			err = json.Unmarshal([]byte(payloadStr), &p)
		}
		if err == nil {
			err = events.PreparePlanBaselined(tx.DB(), taskID, &p)
		}
		payload, summary = p, map[string]any{}
	case events.KindTaskReconciled:
		var p events.TaskReconciledPayload
		if err = json.Unmarshal([]byte(payloadStr), &p); err == nil {
			err = events.PrepareReconciled(tx.DB(), taskID, &p)
		}
		var ids []string
		for _, q := range p.Questions {
			ids = append(ids, fmt.Sprintf("EQ-%d", q.ID))
		}
		payload, summary = p, map[string]any{"seq": p.Seq, "series": p.Series, "ids": ids}
	case events.KindTaskPostLandReconciled:
		var p events.TaskPostLandReconciledPayload
		if err = json.Unmarshal([]byte(payloadStr), &p); err == nil {
			err = events.PreparePostLandReconciled(tx.DB(), taskID, &p)
		}
		var ids []string
		for _, q := range p.Questions {
			ids = append(ids, fmt.Sprintf("EQ-%d", q.ID))
		}
		payload, summary = p, map[string]any{"seq": p.ReconciliationSeq, "attempt": p.Attempt,
			"series": p.Series, "ids": ids}
	}
	if err != nil {
		tx.Rollback()
		// Every preparation refusal is about the caller's input against the
		// task's current items — the caller's to fix, with nothing written.
		return refusal.NoReport(fmt.Sprintf("%s: %s", evtKind, err.Error()),
			"Fix what the message names and retry; nothing was recorded")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("marshal payload: %w", err)
	}

	evt := events.Event{
		V:             events.Version,
		TS:            ts.String(),
		Kind:          evtKind,
		Project:       project,
		Entity:        events.EntityRef{Type: events.EntityTask, ID: entityID},
		Actor:         events.EmittingActor(events.ActorKind(actorKindStr), actorID, sessionID),
		CorrelationID: correlationID,
		Payload:       json.RawMessage(raw),
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
	// None of these kinds changes a task status, so none can cascade into an
	// epic derivation; the apply records none.
	applied, err := tx.Apply(events.Emit{Event: &evt, Writer: writer, Clock: clock})
	if err := finishApply(projectRoot, applied, err); err != nil {
		return err
	}

	out := map[string]any{
		"ts":   ts.String(),
		"kind": string(evtKind),
		"id":   fmt.Sprintf("E-%d", taskID),
	}
	for k, v := range summary {
		out[k] = v
	}
	_ = dbprovenance.Encode(os.Stdout, out)
	return nil
}
