package events

import "fmt"

// Decision removal, per ED-1547 (E-2010).
//
// Removing a decision no longer DELETEs its row: it sets removed = 1 and leaves
// the row in place, so the id is never re-minted. That matters more here than
// almost anywhere else, because decisions have an EXPLICIT high-water allocator
// — `SELECT COALESCE(MAX(id), 0) + 1 FROM decisions` in executor.go — which
// E-1929 named as carrying the same hazard as the task allocator and then
// deliberately left alone. Deleting the highest decision handed its id straight
// to the next one minted, against every ED-NNNN already written into task
// descriptions, plans, commit subjects and CLAUDE.md files.
//
// That allocator must keep reading `decisions`, never live_decisions. Pointed at
// the view it would stop seeing removed rows, MAX(id) would drop back, and the
// reuse this file exists to prevent would return while looking fixed.
//
// This file is the single implementation of the removal, called from BOTH the
// executor (the live write path) and the projector (the ledger-replay rebuild
// path) — including the projector's legacy route, where a pre-E-1378 task
// removal event (then spelled `task.deleted`) whose row lives in `decisions` is
// dispatched here.
// E-1929 learned this the hard way for tasks: a projector that still DELETEd
// while the executor marked removed = 1 would re-free every removed id on
// rebuild, on a path nobody would think to test. Two copies can drift; one
// cannot.

// removeDecision marks decisionID removed and performs the cleanup the FOREIGN
// KEY action used to do for free.
//
// decision_relations.source_decision_id was ON DELETE CASCADE and an UPDATE
// fires no FK action, so the rows this decision owns are deleted explicitly.
// Hard-deleting them is not an exception to ED-1547: a decision_relation's id
// is a surrogate row handle nobody stores or types, which is exactly the class
// the decision leaves unrestricted.
//
// Rows pointing AT the decision from the other direction — `task_deps` with
// target_kind = 'decision', which has no foreign key and so never cascaded —
// are deliberately left in place. They are read through a join to the decision,
// and that join now goes through live_decisions, so the retained row is
// filtered out on read rather than erased on write. This is the half of ED-1547
// that costs something: the join used to drop them by accident because the row
// was gone, and it now has to mean it.
func removeDecision(db dbQuerier, decisionID string) error {
	if _, err := db.Exec(
		"UPDATE decisions SET removed = 1 WHERE id = ?", decisionID,
	); err != nil {
		return fmt.Errorf("events: mark decision %s removed: %w", decisionID, err)
	}

	if _, err := db.Exec(
		"DELETE FROM decision_relations WHERE source_decision_id = ?", decisionID,
	); err != nil {
		return fmt.Errorf("events: drop relations of removed decision %s: %w", decisionID, err)
	}

	return nil
}
