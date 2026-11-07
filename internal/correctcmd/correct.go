// Package correctcmd implements `endless-go correct`, the Go half of
// `endless correct` (E-2486): a user-only command that changes the state of
// anything the ledger tracks, or annotates it, through a change entry and a
// correction entry that records the reason.
//
// The dispatcher (cmd/endless-go) runs monitor.ConsumeDBFlags before Run, as it
// does for `event`: inside a self-dev worktree the command is refused without
// --db, and the ledger follows the database it is pointed at.
//
// One run, in order:
//
//  1. Refuse an agent. The command is the user's, and the refusal is a REPORT:
//     an agent that found the ledger wrong tells the user, who decides.
//  2. BEGIN IMMEDIATE, and prepare the correction against the current rows:
//     the target and its columns, the key, the before values, the gates and the
//     invariants. Anything that refuses, refuses here, with nothing written.
//  3. Take the ledger lock, append the change entry and then the correction
//     entry — adjacent lines, the correction naming the change by byte offset —
//     and commit the segment in one ledger commit whose subject names the
//     correction.
//  4. Apply the change in the open transaction and commit it.
//  5. Rewrite any document mirror the change touched.
//
// The lock order is the one every other emit path keeps: the database lock
// first, the ledger lock inside it.
package correctcmd

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/agentenv"
	"github.com/mikeschinkel/endless/internal/dbprovenance"
	"github.com/mikeschinkel/endless/internal/docsweep"
	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
)

const command = "correct"

// pairList collects repeated col=value flags in order.
type pairList []string

func (p *pairList) String() string     { return strings.Join(*p, ",") }
func (p *pairList) Set(s string) error { *p = append(*p, s); return nil }

// Run is the subcommand entry point. It exits the process.
func Run(args []string) {
	fs := refusal.NewFlags(command)
	target := fs.String("target", "", "What to correct: a table target, or an entity type for an annotation")
	key := fs.String("key", "", "The row's key; composite keys joined by ':'")
	var sets, inserts pairList
	fs.Var(&sets, "set", "col=value to update (repeatable); NULL is SQL NULL")
	fs.Var(&inserts, "insert", "col=value of a new row (repeatable)")
	del := fs.Bool("delete", false, "Delete the row")
	reason := fs.String("reason", "", "Why the correction is made (required)")
	skipGates := fs.Bool("skip-gates", false, "Bypass the lifecycle and vocabulary gates; recorded on the correction")
	project := fs.String("project", "", "Project name")
	projectRoot := fs.String("project-root", "", "Project root directory (main checkout)")
	nodeID := fs.String("node-id", "", "Kairos node ID (4-char hex)")
	actorKind := fs.String("actor-kind", "cli", "Actor kind")
	actorID := fs.String("actor-id", "", "Actor identifier")
	sessionID := fs.String("session-id", "", "Endless session ID (optional)")
	parseFlags(fs, args)

	if err := run(request{
		target: *target, key: *key, sets: sets, inserts: inserts, del: *del,
		reason: *reason, skipGates: *skipGates, project: *project, projectRoot: *projectRoot,
		nodeID: *nodeID, actorKind: *actorKind, actorID: *actorID, sessionID: *sessionID,
	}, os.Stdout); err != nil {
		refusal.From(err).Command(command).Exit(1)
	}
}

// parseFlags classifies the two outcomes a flag set swallows: -h replays the
// usage and exits 0; a bad flag is a NO-REPORT the caller respells.
func parseFlags(fs *refusal.Flags, args []string) {
	err := fs.Parse(args)
	switch {
	case err == nil:
		return
	case errors.Is(err, flag.ErrHelp):
		refusal.Info(fs.Output()).Print()
		os.Exit(0)
	}
	refusal.NoReport(err.Error(), "Correct the flag and retry").
		Command(command).Text(fs.Output()).Exit(2)
}

type request struct {
	target, key   string
	sets, inserts pairList
	del           bool
	reason        string
	skipGates     bool
	project       string
	projectRoot   string
	nodeID        string
	actorKind     string
	actorID       string
	sessionID     string
}

// AgentRefusal is the refusal an agent gets. Exported so the Python command,
// which checks first, and this binary say the same thing.
func AgentRefusal() *refusal.Error {
	return refusal.Report(
		"`endless correct` is the user's command: an agent does not correct the ledger. Nothing was recorded.",
		"whether the recorded state is wrong, and what the correction and its reason should be")
}

func run(r request, out io.Writer) error {
	if agentenv.Present() {
		return AgentRefusal()
	}
	for _, name := range []struct{ flag, value string }{
		{"--target", r.target}, {"--project", r.project}, {"--project-root", r.projectRoot},
		{"--node-id", r.nodeID}, {"--actor-id", r.actorID},
	} {
		if name.value == "" {
			return refusal.NoReport(name.flag+" is required", "Pass "+name.flag+" and retry")
		}
	}
	op, values, err := operation(r)
	if err != nil {
		return err
	}
	nid, err := kairos.ParseNodeID(r.nodeID)
	if err != nil {
		return refusal.Report(
			fmt.Sprintf("invalid node-id: %s", err),
			"whether to repair or remove the malformed node_id in this machine's Endless config.json (a missing one is regenerated)",
		).Cause(err)
	}
	clock := kairos.NewClock(nid)

	lock, err := events.BeginLocked()
	if err != nil {
		return err
	}
	prep, err := events.PrepareCorrection(lock.DB, events.CorrectionRequest{
		Target: r.target, Key: r.key, Op: op, Values: values,
		Reason: r.reason, SkipGates: r.skipGates, Project: r.project,
	})
	if err != nil {
		lock.Rollback()
		return err
	}

	actor := events.EmittingActor(events.ActorKind(r.actorKind), r.actorID, r.sessionID)
	envelope := func(kind events.Kind, payload any) (*events.Event, []byte, error) {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal %s payload: %w", kind, err)
		}
		evt := &events.Event{
			V:       events.Version,
			TS:      clock.Now().String(),
			Kind:    kind,
			Project: r.project,
			Entity:  prep.Entity,
			Actor:   actor,
			Payload: json.RawMessage(raw),
		}
		if err := evt.Validate(); err != nil {
			return nil, nil, err
		}
		line, err := json.Marshal(evt)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal %s event: %w", kind, err)
		}
		return evt, line, nil
	}

	writer, err := events.NewWriter(events.LedgerRoot(r.projectRoot), r.nodeID)
	if err != nil {
		lock.Rollback()
		return fmt.Errorf("create writer: %w", err)
	}
	if err := writer.BeginRun(); err != nil {
		lock.Rollback()
		return err
	}
	defer writer.EndRun()

	var evts []*events.Event
	corrected := events.CorrectedPayload{Reason: r.reason, SkipGates: r.skipGates, Refs: []int64{}}
	if prep.Change != nil {
		evt, line, err := envelope(events.KindLedgerRowChanged, prep.Change)
		if err != nil {
			lock.Rollback()
			return err
		}
		offset, err := writer.AppendOffset(line)
		if err != nil {
			lock.Rollback()
			return err
		}
		evts = append(evts, evt)
		corrected.Refs = append(corrected.Refs, offset)
		corrected.Before = append(corrected.Before, *prep.Before)
	}
	evt, line, err := envelope(events.KindLedgerCorrected, corrected)
	if err != nil {
		lock.Rollback()
		return err
	}
	if _, err := writer.AppendOffset(line); err != nil {
		lock.Rollback()
		return err
	}
	evts = append(evts, evt)

	segment := writer.CurrentSegment()
	if !monitor.IsSandboxActive() {
		segRel := filepath.Join(".endless", events.LedgerDirName, segment)
		if err := events.CommitLedgerSegment(r.projectRoot, segRel); err != nil {
			// The lines are on disk and the row is unchanged; a retry would
			// write them again. Clearing the git failure is the user's.
			return refusal.Report(
				fmt.Sprintf("commit ledger segment: %s", err),
				"how to clear the git failure and reconcile a correction that is in the ledger but not applied",
			).Cause(err)
		}
	}
	writer.EndRun()

	if err := lock.ExecAllAndCommit(evts); err != nil {
		return err
	}

	notes := append([]string{}, prep.Notes...)
	if !monitor.IsSandboxActive() && (len(prep.TaskIDs) > 0 || len(prep.DecisionIDs) > 0) {
		if msg := rewriteMirrors(r.project, r.projectRoot, prep.TaskIDs, prep.DecisionIDs); msg != "" {
			notes = append(notes, msg)
		}
	}

	result := map[string]any{
		"ts":         evt.TS,
		"kind":       string(events.KindLedgerCorrected),
		"entity":     prep.Entity,
		"target":     prep.Target.Name,
		"segment":    segment,
		"refs":       corrected.Refs,
		"skip_gates": r.skipGates,
		"summary":    prep.Summary,
		"notes":      notes,
	}
	if prep.Change != nil {
		result["key"] = prep.Change.Key
		result["op"] = prep.Change.Op
	}
	return dbprovenance.Encode(out, result)
}

// operation reads which of --set, --insert and --delete the caller used, and
// refuses more than one.
func operation(r request) (string, map[string]string, error) {
	used := 0
	if len(r.sets) > 0 {
		used++
	}
	if len(r.inserts) > 0 {
		used++
	}
	if r.del {
		used++
	}
	if used > 1 {
		return "", nil, refusal.NoReport("--set, --insert and --delete are exclusive; a run is one operation",
			"Retry with one of them")
	}
	switch {
	case len(r.sets) > 0:
		values, err := pairs(r.sets, "--set")
		return events.RowOpUpdate, values, err
	case len(r.inserts) > 0:
		values, err := pairs(r.inserts, "--insert")
		return events.RowOpInsert, values, err
	case r.del:
		return events.RowOpDelete, nil, nil
	}
	return "", nil, nil
}

// pairs parses col=value flags. A column named twice is refused rather than
// last-wins: the correction records what was asked, and two values is not one.
func pairs(list pairList, flagName string) (map[string]string, error) {
	out := make(map[string]string, len(list))
	for _, item := range list {
		col, value, ok := strings.Cut(item, "=")
		col = strings.TrimSpace(col)
		if !ok || col == "" {
			return nil, refusal.NoReport(
				fmt.Sprintf("%s takes col=value, got %q", flagName, item),
				"Retry with the column and value joined by =")
		}
		if _, dup := out[col]; dup {
			return nil, refusal.NoReport(
				fmt.Sprintf("%s names %s twice", flagName, col), "Retry naming each column once")
		}
		out[col] = value
	}
	return out, nil
}

// rewriteMirrors rewrites the document mirrors a correction touched. A failure
// is reported as a note rather than failing the command: the row is already
// corrected, and the sweep repairs mirrors on its own cadence.
func rewriteMirrors(project, projectRoot string, taskIDs, decisionIDs []int64) string {
	id, _, err := monitor.ProjectByName(project)
	if err != nil {
		return fmt.Sprintf("Document mirrors were not rewritten: %s", err)
	}
	result, err := docsweep.RewriteMirrors(monitor.ProjectRef{ID: id, Root: projectRoot}, taskIDs, decisionIDs)
	if err != nil {
		return fmt.Sprintf("Document mirrors were not rewritten (the doc-mirrors sweep will): %s", err)
	}
	if result.Total() == 0 {
		return ""
	}
	return fmt.Sprintf("Document mirrors rewritten: %s", result)
}
