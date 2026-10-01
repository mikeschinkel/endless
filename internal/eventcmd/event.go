// Package eventcmd implements the `endless-go event` subcommand.
// The dispatcher (cmd/endless-go) calls monitor.ConsumeDBContextFlag()
// before invoking Run — see E-1429 notes in cmd/endless-go/main.go.
package eventcmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/dbprovenance"
	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/schema"
	"github.com/mikeschinkel/endless/internal/schemachange"
)

func Run(args []string) {
	if len(args) < 1 {
		refusal.NoReport(
			"endless-go event: no command given",
			"Pass one of the listed commands and retry",
		).Command("event").Text(usageText()).Exit(1)
	}

	switch args[0] {
	case "emit":
		runEmit(args[1:])
	case "commit-doc":
		runCommitDoc(args[1:])
	case "validate-db":
		runValidateDB(args[1:])
	case "rebuild-db":
		runRebuildDB(args[1:])
	case "migrate":
		runMigrate()
	case "upgrade":
		runUpgrade()
	case "apply-change":
		runApplyChange(args[1:])
	case "backup":
		runBackup()
	case "reap-worktrees":
		runReapWorktrees(args[1:])
	default:
		// Two readings of the same line, and this binary cannot tell them
		// apart. Typed by hand it is a typo the agent corrects and forgets.
		// Relayed by an `endless` command — commit-doc and migrate are the late
		// verbs most exposed to it — it means the installed endless-go and the
		// Python CLI calling it are different vintages, which only a reinstall
		// fixes. No usage block: this branch has never printed one, and it is
		// also where `event -h` lands.
		refusal.ReportIf(
			fmt.Sprintf("Unknown command: %s", args[0]),
			"this came from an `endless` command rather than a verb you typed",
			"retry with one of the commands endless-go event accepts",
			"the installed endless-go is a different vintage from the endless CLI calling it, and only the user can reinstall a matching pair",
		).Command("event").Exit(1)
	}
}

// usageText is the command list as one block, so a refusal can carry it as
// detail instead of writing it through a second, unclassified path.
func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go event <command> [flags]",
		"Commands: emit, validate-db, rebuild-db, migrate, upgrade, apply-change, backup, reap-worktrees, commit-doc",
	}, "\n") + "\n"
}

// parseFlags parses one verb's flag set and classifies what a plain
// flag.FlagSet used to print from inside the flag package.
//
// refusal.NewFlags is always ContinueOnError (flag.ExitOnError printed and
// exited before the site could say anything), so the two outcomes it used to
// swallow are handled here instead, once, for every verb:
//
//   - -h: not a refusal. flag has already rendered the usage block into the
//     captured buffer, so it is replayed verbatim and the exit stays 0.
//   - a bad flag: NO-REPORT whichever way it was reached. Whether somebody
//     typed it or a version-skewed `endless` relayed it, the next move is the
//     same command spelled correctly, and nothing in it is the user's to
//     decide. fs.Output() is flag's own error line PLUS its usage block, so a
//     person reads exactly what they read before.
func parseFlags(fs *refusal.Flags, command string, args []string) {
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

// exitRelay ends a verb on an error raised somewhere else — internal/events,
// monitor.DB, internal/schema — under the prefix that verb has always printed.
//
// refusal.From is the reason these go through one place: the site that
// CONSTRUCTED the error already chose a class, and a print site holding only an
// error string cannot re-derive it. Anything that never chose one becomes a
// fault, which is the honest answer for this pipeline — an unclassified failure
// between the ledger append and the SQL mutation is Endless breaking, not
// something the agent can retype its way past.
func exitRelay(command, prefix string, err error) {
	refusal.From(err).Command(command).Text(prefix + err.Error()).Exit(1)
}

// runCommitDoc commits a single version-controlled document mirror file
// (E-1747: `.endless/<kind>/<ID>.md`) on the project's main checkout. Backs
// the Python decision-body mirror when `decision add` runs outside any task
// worktree: the decision has no worktree of its own, so its `.md` lands on
// main via the same main-checkout-enforcing commit path the ledger uses.
func runCommitDoc(args []string) {
	fs := refusal.NewFlags("commit-doc")
	projectRoot := fs.String("project-root", "", "Project root directory (main checkout)")
	relPath := fs.String("path", "", "Repo-relative path of the doc file to commit")
	subject := fs.String("subject", "", "Commit subject line")
	parseFlags(fs, "event commit-doc", args)

	if *projectRoot == "" || *relPath == "" || *subject == "" {
		refusal.NoReport(
			"endless-go event commit-doc: --project-root, --path, and --subject are required",
			"Pass all three flags and retry",
		).Command("event commit-doc").Exit(1)
	}
	if err := events.CommitDoc(*projectRoot, *relPath, *subject); err != nil {
		// The mirror commit is the degraded half of `decision add`: the decision
		// row exists and only its .md copy on main is missing. Whether that is
		// something the user has to hear about is decided where the error was
		// built (commit.go), so it is relayed with the class it already carries.
		exitRelay("event commit-doc", "endless-go event commit-doc: error: ", err)
	}
}

func runEmit(args []string) {

	fs := refusal.NewFlags("emit")
	kind := fs.String("kind", "", "Event kind (e.g. task.created)")
	project := fs.String("project", "", "Project name")
	entityType := fs.String("entity-type", "", "Entity type (e.g. task)")
	entityID := fs.String("entity-id", "", "Entity ID")
	actorKind := fs.String("actor-kind", "", "Actor kind (cli, session, hook, system, web)")
	actorID := fs.String("actor-id", "", "Actor identifier")
	sessionID := fs.String("session-id", "", "Endless session ID (optional; sets actor.session_id)")
	nodeID := fs.String("node-id", "", "Kairos node ID (4-char hex)")
	projectRoot := fs.String("project-root", "", "Project root directory (for .endless/db-ledger/)")
	payload := fs.String("payload", "{}", "Event payload as JSON")
	correlationID := fs.String("cid", "", "Correlation ID (optional)")
	tsOverride := fs.String("ts", "", "Historical event timestamp as RFC3339 (default: now). Used by record-only backfills to stamp the real commit date.")

	parseFlags(fs, "event emit", args)

	if err := run(*kind, *project, *entityType, *entityID, *actorKind, *actorID,
		*sessionID, *nodeID, *projectRoot, *payload, *correlationID, *tsOverride); err != nil {
		// THE event-pipeline print site: every emit refusal and fault reaches a
		// user through this one line. One message, many classes — which is why
		// nothing is decided here.
		exitRelay("event emit", "endless-go event: error: ", err)
	}
}

// requiredFlag is the refusal for a flag `event emit` cannot proceed without.
//
// NO-REPORT even though only the Python CLI ever builds this argv, so a missing
// flag means a CLI bug or a version-skewed pair rather than a typo: whichever it
// is, the next move is the same command with the flag supplied, and nothing
// about it is the user's to decide.
func requiredFlag(name string) error {
	return refusal.NoReport(
		fmt.Sprintf("%s is required", name),
		"Pass "+name+" and retry",
	).Command("event emit")
}

// ledgerCommitRefusal is the refusal for a ledger segment that was written to
// disk but could not be committed.
//
// REPORT rather than NO-REPORT because a retry is not safe. The JSONL line is
// already appended and the SQL mutation never ran, so running the command again
// puts a SECOND ledger line down for one intended event — and on the create path
// the BEGIN IMMEDIATE lock is released only by process exit. Clearing the git
// problem and reconciling what is on disk is the user's call.
func ledgerCommitRefusal(err error) error {
	return refusal.Report(
		fmt.Sprintf("commit ledger segment: %s", err),
		"how to clear the git failure and reconcile a ledger line that is on disk but uncommitted, with no matching database mutation",
	).Command("event emit").Cause(err)
}

func run(kindStr, project, entityTypeStr, entityID, actorKindStr, actorID,
	sessionID, nodeIDStr, projectRoot, payloadStr, correlationID, tsOverride string) error {

	// Validate required flags
	if kindStr == "" {
		return requiredFlag("--kind")
	}
	if project == "" {
		return requiredFlag("--project")
	}
	if entityTypeStr == "" {
		return requiredFlag("--entity-type")
	}
	if actorKindStr == "" {
		return requiredFlag("--actor-kind")
	}
	if actorID == "" {
		return requiredFlag("--actor-id")
	}
	if nodeIDStr == "" {
		return requiredFlag("--node-id")
	}
	if projectRoot == "" {
		return requiredFlag("--project-root")
	}

	// Validate kind
	evtKind := events.Kind(kindStr)
	if !events.ValidKinds[evtKind] {
		// Python emits kinds from code and never from agent input, so the two
		// readings are a hand-typed typo and a version-skewed relay — the same
		// pair the unknown-command branch weighs, and for the same reason this
		// binary cannot weigh it alone.
		return refusal.ReportIf(
			fmt.Sprintf("unknown event kind %q", kindStr),
			"this came from an `endless` command rather than a kind you typed",
			"retry with a kind this binary accepts",
			"the installed endless-go is a different vintage from the endless CLI calling it, and only the user can reinstall a matching pair",
		).Command("event emit")
	}

	// Parse node ID and create clock
	nid, err := kairos.ParseNodeID(nodeIDStr)
	if err != nil {
		// node_id is written by the CLI as four hex characters and never typed,
		// so a malformed one is a hand-edited or corrupted config.json. No retry
		// of this command changes that, and deleting it — which regenerates a
		// fresh one — is a decision about the machine's identity in the ledger.
		return refusal.Report(
			fmt.Sprintf("invalid node-id: %s", err),
			"whether to repair or remove the malformed node_id in this machine's Endless config.json (a missing one is regenerated)",
		).Command("event emit").Cause(err)
	}
	clock := kairos.NewClock(nid)
	ts := clock.Now()
	// E-1719: a record-only/historical backfill passes --ts to stamp the real
	// landing (commit) date rather than now(). logical=0 is fine: these are
	// one-shot backfill events with no causal ordering against live traffic.
	if tsOverride != "" {
		parsed, perr := time.Parse(time.RFC3339, tsOverride)
		if perr != nil {
			return refusal.NoReport(
				fmt.Sprintf("invalid --ts %q (want RFC3339, e.g. 2026-05-09T12:34:56Z): %s", tsOverride, perr),
				"Pass an RFC3339 timestamp and retry",
			).Command("event emit").Cause(perr)
		}
		ts = kairos.New(parsed, 0, nid)
	}

	// Determine if this is a create event that needs ID pre-allocation.
	// task.created / task.imported pre-allocate a tasks.id; decision.created
	// (E-1378) pre-allocates a decisions.id and emits an ED-prefixed display
	// form.
	needsTaskPreAlloc := evtKind == events.KindTaskCreated || evtKind == events.KindTaskImported
	needsDecisionPreAlloc := evtKind == events.KindDecisionCreated
	needsPreAlloc := needsTaskPreAlloc || needsDecisionPreAlloc

	if evtKind == events.KindTaskQuestionsAsked {
		return emitQuestionsAsked(ts, project, entityTypeStr, entityID,
			actorKindStr, actorID, sessionID, nodeIDStr, projectRoot, payloadStr,
			correlationID)
	}

	if needsPreAlloc {
		// Events-authoritative flow for creates:
		// 1. Pre-allocate ID (acquires write lock)
		// 2. Build and write event to segment file
		// 3. Execute SQL and commit (releases lock)
		var newID int64
		var execAndCommit func(*events.Event, events.DerivedEmitter) (*events.ExecuteResult, error)
		var rollback func()
		var err error
		if needsTaskPreAlloc {
			newID, execAndCommit, rollback, err = events.PreAllocateTaskID()
		} else {
			newID, execAndCommit, rollback, err = events.PreAllocateDecisionID()
		}
		if err != nil {
			return err
		}

		evt := events.Event{
			V:       events.Version,
			TS:      ts.String(),
			Kind:    evtKind,
			Project: project,
			Entity: events.EntityRef{
				Type: events.EntityType(entityTypeStr),
				ID:   fmt.Sprintf("%d", newID),
			},
			Actor: events.EmittingActor(
				events.ActorKind(actorKindStr), actorID, sessionID),
			CorrelationID: correlationID,
			Payload:       json.RawMessage(payloadStr),
		}

		if err := evt.Validate(); err != nil {
			rollback()
			return err
		}

		// Write event to segment file FIRST (events-authoritative)
		line, err := json.Marshal(evt)
		if err != nil {
			rollback()
			return fmt.Errorf("marshal event: %w", err)
		}

		writer, err := events.NewWriter(ledgerRoot(projectRoot), nodeIDStr)
		if err != nil {
			rollback()
			return fmt.Errorf("create writer: %w", err)
		}
		if err := writer.Append(line); err != nil {
			rollback()
			return err
		}

		// E-1206: commit the just-written ledger segment immediately. Fail
		// loudly on git error; the JSONL line has already been written, so a
		// commit failure surfaces a problem (e.g., not a git repo) without
		// rolling back the WAL. E-1729: the sandbox ledger lives under the
		// disposable, non-git sandbox dir, so skip the commit there.
		if !monitor.IsSandboxActive() {
			segRel := filepath.Join(".endless", events.LedgerDirName, writer.CurrentSegment())
			if err := events.CommitLedgerSegment(projectRoot, segRel); err != nil {
				return ledgerCommitRefusal(err)
			}
		}

		// E-1541: only task creates can add a child to a parent epic and so
		// trigger derivation; decision creates never do.
		var emit events.DerivedEmitter
		if needsTaskPreAlloc {
			emit = makeDerivedEmitter(clock, project, nodeIDStr, projectRoot, writer)
		}

		// Execute SQL mutation and commit (releases write lock)
		if _, err := execAndCommit(&evt, emit); err != nil {
			return err
		}

		idPrefix := "E-"
		if needsDecisionPreAlloc {
			idPrefix = "ED-"
		}
		output := map[string]string{
			"ts":   ts.String(),
			"kind": kindStr,
			"id":   fmt.Sprintf("%s%d", idPrefix, newID),
		}
		// E-1668: a write names the database it landed in. E-1429's founding
		// incident was a write — a test `endless task add` that became E-1425 in
		// the REAL ledger — and a write to the wrong store is the damaging case,
		// where a wrong read only misleads.
		_ = dbprovenance.Encode(os.Stdout, output)

	} else {
		// Events-authoritative flow for updates/deletes:
		// 1. Build event (entity ID already known)
		// 2. Write event to segment file
		// 3. Execute SQL mutation
		evt := events.Event{
			V:       events.Version,
			TS:      ts.String(),
			Kind:    evtKind,
			Project: project,
			Entity: events.EntityRef{
				Type: events.EntityType(entityTypeStr),
				ID:   entityID,
			},
			Actor: events.EmittingActor(
				events.ActorKind(actorKindStr), actorID, sessionID),
			CorrelationID: correlationID,
			Payload:       json.RawMessage(payloadStr),
		}

		if err := evt.Validate(); err != nil {
			return err
		}
		// E-2176: a refused question move must not reach the ledger.
		if evtKind == events.KindTaskQuestionResolved {
			if err := events.PrecheckTaskQuestionResolved(&evt); err != nil {
				return err
			}
		}

		// Write event to segment file FIRST (events-authoritative)
		line, err := json.Marshal(evt)
		if err != nil {
			return fmt.Errorf("marshal event: %w", err)
		}

		writer, err := events.NewWriter(ledgerRoot(projectRoot), nodeIDStr)
		if err != nil {
			return fmt.Errorf("create writer: %w", err)
		}
		if err := writer.Append(line); err != nil {
			return err
		}

		// E-1206: commit the just-written ledger segment immediately.
		// E-1729: skip the commit in a sandbox (disposable, non-git dir).
		if !monitor.IsSandboxActive() {
			segRel := filepath.Join(".endless", events.LedgerDirName, writer.CurrentSegment())
			if err := events.CommitLedgerSegment(projectRoot, segRel); err != nil {
				return ledgerCommitRefusal(err)
			}
		}

		// Execute SQL mutation (side effect of the event). The derived emitter
		// (E-1541) records any epic.status_derived entries the mutation
		// cascades into, on the same writer, after this primary event.
		emit := makeDerivedEmitter(clock, project, nodeIDStr, projectRoot, writer)
		execRes, err := events.Execute(&evt, emit)
		if err != nil {
			return err
		}

		// E-1312: include ExecuteResult fields so callers (e.g. the
		// Python session_status_cmd CLI) can render the result for chat.
		// Switching to map[string]any so non-string fields serialize cleanly.
		output := map[string]any{
			"ts":   ts.String(),
			"kind": kindStr,
		}
		if execRes != nil {
			if execRes.SessionStatusID != 0 {
				output["session_status_id"] = execRes.SessionStatusID
			}
			if execRes.Skipped {
				output["skipped"] = true
			}
			if execRes.Markdown != "" {
				output["markdown"] = execRes.Markdown
			}
		}
		// E-1668: a write names the database it landed in. E-1429's founding
		// incident was a write — a test `endless task add` that became E-1425 in
		// the REAL ledger — and a write to the wrong store is the damaging case,
		// where a wrong read only misleads.
		_ = dbprovenance.Encode(os.Stdout, output)
	}

	return nil
}

// ledgerRoot returns the root directory whose .endless/db-ledger/ holds the
// event ledger for the active DB context (ED-1525: the ledger follows the DB).
// In an E-1281 sandbox (ConfigDir() resolves under CacheDir()/sandboxes/, the
// sole sandbox switch per ED-1528) the ledger lives beside the sandbox
// endless.db — at ConfigDir()/.endless/db-ledger — so sandbox emits and reads
// never touch the real project ledger. Otherwise it is projectRoot, the real
// checkout. NewWriter/ReadAllEvents append .endless/db-ledger to whatever root
// they receive, and NewWriter MkdirAll's it, so the sandbox ledger dir is
// created on first write.
func ledgerRoot(projectRoot string) string {
	if monitor.IsSandboxActive() {
		return monitor.ConfigDir()
	}
	return projectRoot
}

// makeDerivedEmitter builds the epic-derivation ledger emitter (E-1541) threaded
// into the executor. When the executor's recompute changes an epic's status it
// calls this closure, which builds an epic.status_derived event (fresh kairos
// timestamp from the same clock, system/epic-derivation actor), appends it to
// the same ledger segment writer as the triggering event, and commits it. The
// emit fires inside the triggering mutation's open SQL transaction — same
// segment-before-SQL-commit ordering as the primary event (E-1206).
func makeDerivedEmitter(clock *kairos.Clock, project, nodeIDStr, projectRoot string,
	writer *events.Writer) events.DerivedEmitter {

	return func(epicID int64, oldStatus, newStatus string) error {
		payload, err := json.Marshal(events.EpicStatusDerivedPayload{
			TaskID:    epicID,
			OldStatus: oldStatus,
			NewStatus: newStatus,
		})
		if err != nil {
			return fmt.Errorf("marshal epic.status_derived payload: %w", err)
		}
		evt := events.Event{
			V:       events.Version,
			TS:      clock.Now().String(),
			Kind:    events.KindEpicStatusDerived,
			Project: project,
			Entity: events.EntityRef{
				Type: events.EntityTask,
				ID:   fmt.Sprintf("%d", epicID),
			},
			// Actor built literally rather than via events.EmittingActor:
			// this event is synthesized by Endless, not typed by anyone, so
			// stamping the emitting process's harness onto it would answer a
			// question it was never asked (E-2005).
			Actor: events.Actor{
				Kind: events.ActorSystem,
				ID:   "epic-derivation",
			},
			Payload: payload,
		}
		if err := evt.Validate(); err != nil {
			return err
		}
		line, err := json.Marshal(evt)
		if err != nil {
			return fmt.Errorf("marshal epic.status_derived event: %w", err)
		}
		if err := writer.Append(line); err != nil {
			return err
		}
		// E-1729: skip the commit in a sandbox (disposable, non-git dir). The
		// derived event is already appended to the sandbox ledger via `writer`.
		if monitor.IsSandboxActive() {
			return nil
		}
		segRel := filepath.Join(".endless", events.LedgerDirName, writer.CurrentSegment())
		return events.CommitLedgerSegment(projectRoot, segRel)
	}
}

func runValidateDB(args []string) {
	fs := refusal.NewFlags("validate-db")
	projectRoot := fs.String("project-root", "", "Project root directory")
	parseFlags(fs, "event validate-db", args)

	if *projectRoot == "" {
		refusal.NoReport(
			"endless-go event: error: --project-root is required",
			"Pass --project-root and retry",
		).Command("event validate-db").Exit(1)
	}

	// Get schema from current DB
	// Project events into temp DB. E-1729: read the ledger for the active DB
	// context so validate-db in a sandbox replays the sandbox ledger.
	tempPath, projResult, err := events.ProjectToTempDB(ledgerRoot(*projectRoot))
	if err != nil {
		exitRelay("event validate-db", "endless-go event: error: ", err)
	}
	defer os.Remove(tempPath)

	fmt.Printf("Projection: %d events replayed, %d tasks created, %d updated, %d deleted\n",
		projResult.EventsReplayed, projResult.TasksCreated, projResult.TasksUpdated, projResult.TasksDeleted)
	for _, e := range projResult.Errors {
		fmt.Printf("  warning: %s\n", e)
	}

	// Compare against current DB
	currentDB, err := monitor.DB()
	if err != nil {
		exitRelay("event validate-db", "endless-go event: error: ", err)
	}

	valResult, err := events.ValidateTasks(currentDB, tempPath)
	if err != nil {
		exitRelay("event validate-db", "endless-go event: error: ", err)
	}

	fmt.Printf("Validation: %d tasks compared\n", valResult.TasksCompared)

	if len(valResult.MissingTasks) > 0 {
		fmt.Printf("\nMissing tasks (%d):\n", len(valResult.MissingTasks))
		for _, m := range valResult.MissingTasks {
			fmt.Printf("  E-%d (%s): only in %s\n", m.TaskID, m.Title, m.In)
		}
	}

	if len(valResult.Mismatches) > 0 {
		fmt.Printf("\nMismatches (%d):\n", len(valResult.Mismatches))
		for _, m := range valResult.Mismatches {
			fmt.Printf("  E-%d %s: projected=%q current=%q\n",
				m.TaskID, m.Field, m.Projected, m.Current)
		}
	}

	if len(valResult.MissingTasks) == 0 && len(valResult.Mismatches) == 0 {
		fmt.Println("\nAll projected tasks match current DB state.")
	}
}

func runRebuildDB(args []string) {
	fs := refusal.NewFlags("rebuild-db")
	projectRoot := fs.String("project-root", "", "Project root directory")
	confirm := fs.Bool("confirm", false, "DISABLED (E-2062): refuses and reports what replacing the tasks table would destroy")
	parseFlags(fs, "event rebuild-db", args)

	if *projectRoot == "" {
		refusal.NoReport(
			"endless-go event: error: --project-root is required",
			"Pass --project-root and retry",
		).Command("event rebuild-db").Exit(1)
	}

	// E-2062: refuse --confirm here, before the projection is built and before
	// any transaction opens, so the refusal costs nothing and can leave nothing
	// behind. Everything below this line — the replay, the ATTACH, the DELETE —
	// is dry-run-only until E-799 makes the copy-back whole. The reasoning,
	// including why relaxing the schema to make this work is the wrong order,
	// is at the top of rebuild_guard.go. Does not return.
	if *confirm {
		refuseRebuildDBConfirm()
	}

	tempPath, projResult, err := events.ProjectToTempDB(ledgerRoot(*projectRoot))
	if err != nil {
		exitRelay("event rebuild-db", "endless-go event: error: ", err)
	}

	fmt.Printf("Projection: %d events replayed, %d tasks created, %d updated, %d deleted\n",
		projResult.EventsReplayed, projResult.TasksCreated, projResult.TasksUpdated, projResult.TasksDeleted)
	for _, e := range projResult.Errors {
		fmt.Printf("  warning: %s\n", e)
	}

	if !*confirm {
		// The only path this command has. --confirm never reaches here: the
		// E-2062 guard above exits before the projection is built, so the banner
		// no longer advertises a flag that refuses.
		fmt.Println("\nDry run — this command is read-only. --confirm is disabled (E-2062).")
		os.Remove(tempPath)
		return
	}

	// Everything from here down is unreachable and kept only for E-799 to
	// repair: --confirm exits inside refuseRebuildDBConfirm above, and the dry
	// run has already returned. Nothing normally reads these lines, so they are
	// classified by what reaching them WOULD mean — a copy-back that got past
	// the guard built to stop it, which is Endless broken rather than anything a
	// caller can retype. The relayed ones stay relays: an error raised elsewhere
	// keeps the class its own site chose even here.
	currentDB, err := monitor.DB()
	if err != nil {
		os.Remove(tempPath)
		exitRelay("event rebuild-db", "endless-go event: error: ", err)
	}

	if _, err := currentDB.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS proj", tempPath)); err != nil {
		os.Remove(tempPath)
		rebuildFault("endless-go event: error attaching temp db: %v", err)
	}

	tx, err := currentDB.Begin()
	if err != nil {
		os.Remove(tempPath)
		rebuildFault("endless-go event: error: %v", err)
	}

	// Delete current tasks for this project and insert from projection.
	//
	// Raw `tasks` on both sides, never live_tasks (E-1929): this is a wholesale
	// replacement of the table from the ledger, so removed rows must be cleared
	// and re-inserted with their removed flag intact. Filtering either side would
	// drop the retained rows and re-free their ids — the failure ED-1547 names.
	// `SELECT *` carries the flag across because schema.sql declares `removed`
	// last on tasks, the same position ALTER TABLE gives it on a migrated DB.
	if _, err := tx.Exec("DELETE FROM tasks WHERE project_id IN (SELECT id FROM projects WHERE name IN (SELECT name FROM proj.projects))"); err != nil {
		tx.Rollback()
		os.Remove(tempPath)
		rebuildFault("endless-go event: error clearing tasks: %v", err)
	}

	if _, err := tx.Exec("INSERT INTO tasks SELECT * FROM proj.tasks"); err != nil {
		tx.Rollback()
		os.Remove(tempPath)
		rebuildFault("endless-go event: error inserting projected tasks: %v", err)
	}

	// E-1378: also replace decisions and decision_relations from the projection.
	// decision_relations rows reference decisions(id) with ON DELETE CASCADE,
	// so deleting decisions first clears its relations atomically.
	if _, err := tx.Exec("DELETE FROM decisions WHERE project_id IN (SELECT id FROM projects WHERE name IN (SELECT name FROM proj.projects))"); err != nil {
		tx.Rollback()
		os.Remove(tempPath)
		rebuildFault("endless-event: error clearing decisions: %v", err)
	}

	if _, err := tx.Exec("INSERT INTO decisions SELECT * FROM proj.decisions"); err != nil {
		tx.Rollback()
		os.Remove(tempPath)
		rebuildFault("endless-event: error inserting projected decisions: %v", err)
	}

	// decision_relations: same project filter, joined via decisions.project_id.
	// The previous DELETE FROM decisions has already cleared the corresponding
	// rows via the FK cascade; insert the projected rows fresh.
	if _, err := tx.Exec("INSERT INTO decision_relations SELECT * FROM proj.decision_relations"); err != nil {
		tx.Rollback()
		os.Remove(tempPath)
		rebuildFault("endless-event: error inserting projected decision_relations: %v", err)
	}

	if err := tx.Commit(); err != nil {
		os.Remove(tempPath)
		rebuildFault("endless-go event: error committing: %v", err)
	}

	currentDB.Exec("DETACH DATABASE proj")
	os.Remove(tempPath)
	fmt.Printf("Rebuilt: tasks table replaced with %d projected tasks.\n", projResult.TasksCreated)
}

// rebuildFault ends the dead copy-back path. See the comment at that path's
// head for why every failure in it is a fault and why the path exists at all.
func rebuildFault(format string, args ...any) {
	refusal.Faultf(format, args...).Command("event rebuild-db").Exit(1)
}

func runReapWorktrees(args []string) {
	fs := refusal.NewFlags("reap-worktrees")
	projectRoot := fs.String("project-root", "", "Project root directory")
	ttlOverride := fs.String("ttl", "", "TTL override (default: read from .endless/config.json, fallback 14d)")
	parseFlags(fs, "event reap-worktrees", args)

	if *projectRoot == "" {
		refusal.NoReport(
			"endless-go event: reap-worktrees: --project-root is required",
			"Pass --project-root and retry",
		).Command("event reap-worktrees").Exit(1)
	}

	ttlStr := *ttlOverride
	if ttlStr == "" {
		ttlStr = monitor.ReadWorktreeTTLConfig(*projectRoot)
	}
	ttl := monitor.DefaultWorktreeTTL
	if ttlStr != "" {
		parsed, err := monitor.ParseWorktreeTTL(ttlStr)
		if err != nil {
			// Nothing is blocked — the sweep runs on the default TTL — and the
			// value is in the project's own .endless/config.json, which no
			// retry of this command changes and no agent should be editing on
			// its own. So it goes to the user through the errors channel rather
			// than to whoever happened to trigger the sweep (E-2159 decision 5).
			//
			// Fingerprinted on the offending value, so one bad config is one
			// incident however many sweeps read it.
			faults.Record(faults.Fault{
				Code:        faults.ErrCodeWorktreeTTLUnreadable,
				Source:      "event:reap-worktrees",
				Fingerprint: "worktree_ttl=" + ttlStr,
				Summary: fmt.Sprintf("worktree_ttl %q did not parse; sweeping on the default %s",
					ttlStr, monitor.DefaultWorktreeTTL),
				Detail: fmt.Sprintf("parse ttl %q: %v", ttlStr, err),
				Fields: map[string]any{"value": ttlStr, "default": monitor.DefaultWorktreeTTL.String()},
			})
		} else {
			ttl = parsed
		}
	}

	// context.Background(): `endless-go event reap-worktrees` is a one-shot CLI
	// invocation with no ambient context to inherit, and the reaper's git probes
	// now take one (E-2128). This is where the chain terminates.
	if err := monitor.ReapStaleWorktrees(context.Background(), *projectRoot, ttl); err != nil {
		// The sweep has two callers with different stakes, and this process
		// cannot tell them apart: `task claim` and `worktree land` run it
		// best-effort after their own work, while the sweep verb IS the sweep.
		// Only the agent, which holds the conversation, knows which.
		refusal.ReportIf(
			fmt.Sprintf("endless-go event: reap-worktrees: %v", err),
			"the user asked for the stale-worktree sweep itself rather than reaching it through a claim or a land",
			"carry on without it: after a claim or a land the sweep is best-effort",
			"the sweep the user asked for did not run",
		).Command("event reap-worktrees").Exit(1)
	}
}

// runApplyChange applies one per-ticket schema-change file
// (internal/schema/changes/<name>.{sql,go}) and records it in _schema_version.
//
// This is the INSTALLED binary's path, and outside self_dev the only one: it
// opens through monitor.DB(), the application's connect, which checks the
// schema version (E-2020), seeds the enum mirrors and runs the fail-closed
// integrity gates before a change is applied at all.
//
// In self_dev at land time it is NOT the path. ED-1567 forbids a candidate
// binary migrating the real ledger and a self_dev land only ever has one
// (E-1664), so `worktree land` runs ED-1571's cmd/endless-migrate instead — the
// same internal/schemachange logic reached through a direct file open rather
// than through this connect. The difference between the two programs is exactly
// that handle, which is why the applying itself lives in one place.
func runApplyChange(args []string) {
	fs := refusal.NewFlags("apply-change")
	parseFlags(fs, "event apply-change", args)

	pos := fs.Args()
	if len(pos) != 1 {
		emitChangeErr("", "apply-change requires exactly one <path> argument")
	}
	path, err := filepath.Abs(pos[0])
	if err != nil {
		emitChangeErr("", fmt.Sprintf("resolve path: %v", err))
	}

	db, err := monitor.DB()
	if err != nil {
		emitChangeErr(schemachange.Name(dt.Filepath(path)), fmt.Sprintf("open db: %v", err))
	}

	// A .go change's log lines are its own output, relayed unchanged: this
	// process's stdout is one JSON document and nothing else, so they have
	// nowhere to go but stderr, and refusal.Passthrough is how a child's stream
	// is handed out deliberately rather than by oversight.
	res, err := schemachange.Apply(db, dt.Filepath(monitor.DBPath()), dt.Filepath(path), refusal.Passthrough())
	if err != nil {
		emitChangeErr(res.Name, err.Error())
	}
	emitChangeResult(res.Name, string(res.Status), res.Reason)
}

// runBackup reports the destination path so the CLI can name the file it just
// wrote. "skipped" means a backup newer than the throttle window already
// existed and `path` is that one — the caller must not claim it wrote it (E-1942).
//
// BackupDB also enforces retention, and folds a failure of EITHER half into one
// error. Which half is readable from the result: Path is empty only when no
// backup exists. A land runs this unattended before applying a schema change, so
// a failed unlink must not abort it — the copy the land needs is on disk. The
// retention failure rides out as a warning instead, and the Python CLI prints
// it, because a directory that has stopped being pruned is worth saying out loud
// exactly once rather than never (E-2121).
// runMigrate creates the database at the resolved DB context if it does not
// exist, and reports the version it is at.
//
// It exists so the Python CLI can build a database without owning a migration
// runner. db.py used to read internal/schema/schema.sql off disk and
// executescript() it, which meant two programs applied the schema by two
// mechanisms and only one of them could be told about a new migration.
//
// The work itself is monitor.DB(), so this verb inherits the connect's rules
// (E-2020) rather than routing around them: a fresh file is behind and is built
// forward, a database behind this binary is backed up and migrated, one ahead
// of it is refused, and a worktree build never opens main. The explicit,
// gate-free path is runUpgrade.
func runMigrate() {
	// Three relays rather than three classifications: the connect IS the
	// migration (E-2019), so what fails here is monitor.DB's DB-context refusal,
	// the E-1818 schema-passive gate, or a migration failure — each already
	// classified where it was raised, and each reaching a user through
	// event_bridge's "schema initialization failed".
	db, err := monitor.DB()
	if err != nil {
		exitRelay("event migrate", "endless-go event migrate: ", err)
	}
	version, err := schema.DBVersion(context.Background(), db)
	if err != nil {
		exitRelay("event migrate", "endless-go event migrate: ", err)
	}
	latest, err := schema.LatestVersion()
	if err != nil {
		exitRelay("event migrate", "endless-go event migrate: ", err)
	}
	b, _ := json.Marshal(map[string]any{
		"status":  "ok",
		"db":      monitor.DBPath(),
		"version": version,
		"latest":  latest,
	})
	fmt.Println(string(b))
}

// runUpgrade is `endless db upgrade` (E-2020): back the database up, bring it
// to this binary's latest version, reseed the enum mirrors, and report the
// versions either side.
//
// It is the RECOVERY command, so it must not depend on the path it recovers.
// monitor.DB() refuses a database at the wrong version and fail-closes one whose
// enum mirrors have drifted; every command that connects through it fails then,
// `event migrate` included, since that verb IS the connect. So this opens the
// database FILE (schema.OpenExisting), the way `endless-migrate up` does, and
// runs the same schema.Up. What it keeps of the connect is only the question of
// WHICH database: the E-1429 worktree gate and ED-1601's refusal of a worktree
// build aimed at main (monitor.UpgradeTarget).
//
// The backup comes first and is not optional: a binary may not write a database
// ahead of it (ED-1570), so restoring this backup is the only way back from a
// bad release.
func runUpgrade() {
	// Every failure here goes out through the package's relay funnel, so a
	// class chosen at the source survives the trip: monitor.UpgradeTarget's own
	// refusals — the E-1429 worktree gate, ED-1601's refusal of a worktree
	// build aimed at main — name their own fix and stay NO-REPORT. Everything
	// else faults, which is the right default for the RECOVERY command: if the
	// upgrade did not happen the database is still at the version nothing can
	// connect to, and there is no second way for the agent to ask for it.
	fail := func(err error) {
		exitRelay("event upgrade", "endless-go event upgrade: ", err)
	}

	path, err := monitor.UpgradeTarget()
	if err != nil {
		fail(err)
	}
	backup, err := monitor.BackupDB()
	if err != nil && backup.Path == "" {
		fail(fmt.Errorf("backing up before upgrading: %w", err))
	}
	db, err := schema.OpenExisting(dt.Filepath(path))
	if err != nil {
		fail(err)
	}
	defer db.Close()

	res, err := schema.Up(context.Background(), db)
	if err != nil {
		fail(fmt.Errorf("upgrading %s (backup at %s): %w", path, backup.Path, err))
	}
	b, _ := json.Marshal(map[string]any{
		"status":         res.Status,
		"from":           res.From,
		"to":             res.To,
		"db":             path,
		"backup":         backup.Path,
		"backup_skipped": backup.Skipped,
	})
	fmt.Println(string(b))
}

func runBackup() {
	payload := map[string]any{}
	res, err := monitor.BackupDB()
	if err != nil && res.Path == "" {
		// Path empty means no backup exists at all, so a land that runs this
		// before applying a schema change has nothing to fall back to. Disk,
		// permissions or a database failure — none of them is something a retry
		// clears, and proceeding without a backup is the user's call.
		refusal.Report(
			fmt.Sprintf("endless-go event backup: %v", err),
			"how to clear a database that could not be backed up — disk, permissions or a DB failure — before any schema change is applied",
		).Command("event backup").Exit(1)
	}
	if err != nil {
		payload["warning"] = err.Error()
	}
	status := "ok"
	if res.Skipped {
		status = "skipped"
	}
	payload["status"] = status
	payload["path"] = res.Path
	payload["pruned"] = res.Pruned
	_ = dbprovenance.Encode(os.Stdout, payload)
}

func emitChangeResult(name, status, reason string) {
	out := map[string]any{"name": name, "status": status}
	if reason != "" {
		out["reason"] = reason
	}
	_ = dbprovenance.Encode(os.Stdout, out)
}

func emitChangeErr(name, msg string) {
	out := map[string]any{"status": "error", "error": msg}
	if name != "" {
		out["name"] = name
	}
	_ = dbprovenance.Encode(os.Stdout, out)
	os.Exit(1)
}
