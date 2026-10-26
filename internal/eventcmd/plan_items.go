package eventcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/dbprovenance"
	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/monitor"
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
// The shape of emitQuestionsAsked — lock, prepare, append, commit the segment,
// execute, release — with the preparation chosen by kind. A refusal from
// preparation leaves nothing behind: the lock is rolled back before anything
// reaches the ledger, so a refused change can never become a line every replay
// then fails on.
func emitPrepared(evtKind events.Kind, ts kairos.Timestamp, project,
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

	lock, err := events.BeginLocked()
	if err != nil {
		return err
	}

	var payload any
	var summary map[string]any
	switch evtKind {
	case events.KindTaskPlanItemsChanged:
		var p events.TaskPlanItemsChangedPayload
		if err = json.Unmarshal([]byte(payloadStr), &p); err == nil {
			err = events.PreparePlanItemsChangedBy(lock.DB, taskID, &p, events.DetectedHarness() != "")
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
			err = events.PreparePlanBaselined(lock.DB, taskID, &p)
		}
		payload, summary = p, map[string]any{}
	case events.KindTaskReconciled:
		var p events.TaskReconciledPayload
		if err = json.Unmarshal([]byte(payloadStr), &p); err == nil {
			err = events.PrepareReconciled(lock.DB, taskID, &p)
		}
		var ids []string
		for _, q := range p.Questions {
			ids = append(ids, fmt.Sprintf("EQ-%d", q.ID))
		}
		payload, summary = p, map[string]any{"seq": p.Seq, "series": p.Series, "ids": ids}
	case events.KindTaskPostLandReconciled:
		var p events.TaskPostLandReconciledPayload
		if err = json.Unmarshal([]byte(payloadStr), &p); err == nil {
			err = events.PreparePostLandReconciled(lock.DB, taskID, &p)
		}
		var ids []string
		for _, q := range p.Questions {
			ids = append(ids, fmt.Sprintf("EQ-%d", q.ID))
		}
		payload, summary = p, map[string]any{"seq": p.ReconciliationSeq, "attempt": p.Attempt,
			"series": p.Series, "ids": ids}
	}
	if err != nil {
		lock.Rollback()
		// Every preparation refusal is about the caller's input against the
		// task's current items — the caller's to fix, with nothing written.
		return refusal.NoReport(fmt.Sprintf("%s: %s", evtKind, err.Error()),
			"Fix what the message names and retry; nothing was recorded")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		lock.Rollback()
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
		lock.Rollback()
		return err
	}
	line, err := json.Marshal(evt)
	if err != nil {
		lock.Rollback()
		return fmt.Errorf("marshal event: %w", err)
	}
	writer, err := events.NewWriter(ledgerRoot(projectRoot), nodeIDStr)
	if err != nil {
		lock.Rollback()
		return fmt.Errorf("create writer: %w", err)
	}
	if err := writer.Append(line); err != nil {
		lock.Rollback()
		return err
	}
	if !monitor.IsSandboxActive() {
		segRel := filepath.Join(".endless", events.LedgerDirName, writer.CurrentSegment())
		if err := events.CommitLedgerSegment(projectRoot, segRel); err != nil {
			return fmt.Errorf("commit ledger segment: %w", err)
		}
	}
	// None of these kinds changes a task status, so none can cascade into an
	// epic derivation: no derived emitter.
	if _, err := lock.ExecAndCommit(&evt, nil); err != nil {
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
