package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/monitor"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: endless-event <command> [flags]\n")
		fmt.Fprintf(os.Stderr, "Commands: emit, validate-db, rebuild-db, apply-change, backup, reap-worktrees\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "emit":
		runEmit()
	case "validate-db":
		runValidateDB()
	case "rebuild-db":
		runRebuildDB()
	case "apply-change":
		runApplyChange()
	case "backup":
		runBackup()
	case "reap-worktrees":
		runReapWorktrees()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runEmit() {

	fs := flag.NewFlagSet("emit", flag.ExitOnError)
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

	fs.Parse(os.Args[2:])

	if err := run(*kind, *project, *entityType, *entityID, *actorKind, *actorID,
		*sessionID, *nodeID, *projectRoot, *payload, *correlationID); err != nil {
		fmt.Fprintf(os.Stderr, "endless-event: error: %v\n", err)
		os.Exit(1)
	}
}

func run(kindStr, project, entityTypeStr, entityID, actorKindStr, actorID,
	sessionID, nodeIDStr, projectRoot, payloadStr, correlationID string) error {

	// Validate required flags
	if kindStr == "" {
		return fmt.Errorf("--kind is required")
	}
	if project == "" {
		return fmt.Errorf("--project is required")
	}
	if entityTypeStr == "" {
		return fmt.Errorf("--entity-type is required")
	}
	if actorKindStr == "" {
		return fmt.Errorf("--actor-kind is required")
	}
	if actorID == "" {
		return fmt.Errorf("--actor-id is required")
	}
	if nodeIDStr == "" {
		return fmt.Errorf("--node-id is required")
	}
	if projectRoot == "" {
		return fmt.Errorf("--project-root is required")
	}

	// Validate kind
	evtKind := events.Kind(kindStr)
	if !events.ValidKinds[evtKind] {
		return fmt.Errorf("unknown event kind %q", kindStr)
	}

	// Parse node ID and create clock
	nid, err := kairos.ParseNodeID(nodeIDStr)
	if err != nil {
		return fmt.Errorf("invalid node-id: %w", err)
	}
	clock := kairos.NewClock(nid)
	ts := clock.Now()

	// Determine if this is a create event that needs ID pre-allocation
	needsPreAlloc := evtKind == events.KindTaskCreated || evtKind == events.KindTaskImported

	if needsPreAlloc {
		// Events-authoritative flow for creates:
		// 1. Pre-allocate ID (acquires write lock)
		// 2. Build and write event to segment file
		// 3. Execute SQL and commit (releases lock)
		taskID, execAndCommit, rollback, err := events.PreAllocateTaskID()
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
				ID:   fmt.Sprintf("%d", taskID),
			},
			Actor: events.Actor{
				Kind:      events.ActorKind(actorKindStr),
				ID:        actorID,
				SessionID: sessionID,
			},
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

		writer, err := events.NewWriter(projectRoot, nodeIDStr)
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
		// rolling back the WAL.
		segRel := filepath.Join(".endless", events.LedgerDirName, writer.CurrentSegment())
		if err := events.CommitLedgerSegment(projectRoot, segRel); err != nil {
			return fmt.Errorf("commit ledger segment: %w", err)
		}

		// Execute SQL mutation and commit (releases write lock)
		if _, err := execAndCommit(&evt); err != nil {
			return err
		}

		output := map[string]string{
			"ts":   ts.String(),
			"kind": kindStr,
			"id":   fmt.Sprintf("E-%d", taskID),
		}
		outJSON, _ := json.Marshal(output)
		fmt.Println(string(outJSON))

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
			Actor: events.Actor{
				Kind:      events.ActorKind(actorKindStr),
				ID:        actorID,
				SessionID: sessionID,
			},
			CorrelationID: correlationID,
			Payload:       json.RawMessage(payloadStr),
		}

		if err := evt.Validate(); err != nil {
			return err
		}

		// Write event to segment file FIRST (events-authoritative)
		line, err := json.Marshal(evt)
		if err != nil {
			return fmt.Errorf("marshal event: %w", err)
		}

		writer, err := events.NewWriter(projectRoot, nodeIDStr)
		if err != nil {
			return fmt.Errorf("create writer: %w", err)
		}
		if err := writer.Append(line); err != nil {
			return err
		}

		// E-1206: commit the just-written ledger segment immediately.
		segRel := filepath.Join(".endless", events.LedgerDirName, writer.CurrentSegment())
		if err := events.CommitLedgerSegment(projectRoot, segRel); err != nil {
			return fmt.Errorf("commit ledger segment: %w", err)
		}

		// Execute SQL mutation (side effect of the event).
		execRes, err := events.Execute(&evt)
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
		outJSON, _ := json.Marshal(output)
		fmt.Println(string(outJSON))
	}

	return nil
}

func runValidateDB() {
	fs := flag.NewFlagSet("validate-db", flag.ExitOnError)
	projectRoot := fs.String("project-root", "", "Project root directory")
	fs.Parse(os.Args[2:])

	if *projectRoot == "" {
		fmt.Fprintf(os.Stderr, "endless-event: error: --project-root is required\n")
		os.Exit(1)
	}

	// Get schema from current DB
	// Project events into temp DB
	tempPath, projResult, err := events.ProjectToTempDB(*projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-event: error: %v\n", err)
		os.Exit(1)
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
		fmt.Fprintf(os.Stderr, "endless-event: error: %v\n", err)
		os.Exit(1)
	}

	valResult, err := events.ValidateTasks(currentDB, tempPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-event: error: %v\n", err)
		os.Exit(1)
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

func runRebuildDB() {
	fs := flag.NewFlagSet("rebuild-db", flag.ExitOnError)
	projectRoot := fs.String("project-root", "", "Project root directory")
	confirm := fs.Bool("confirm", false, "Actually replace the tasks table (without this, just shows what would happen)")
	fs.Parse(os.Args[2:])

	if *projectRoot == "" {
		fmt.Fprintf(os.Stderr, "endless-event: error: --project-root is required\n")
		os.Exit(1)
	}

	tempPath, projResult, err := events.ProjectToTempDB(*projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-event: error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Projection: %d events replayed, %d tasks created, %d updated, %d deleted\n",
		projResult.EventsReplayed, projResult.TasksCreated, projResult.TasksUpdated, projResult.TasksDeleted)
	for _, e := range projResult.Errors {
		fmt.Printf("  warning: %s\n", e)
	}

	if !*confirm {
		fmt.Println("\nDry run. Use --confirm to replace the tasks table.")
		os.Remove(tempPath)
		return
	}

	// Replace tasks table in current DB from temp DB
	currentDB, err := monitor.DB()
	if err != nil {
		os.Remove(tempPath)
		fmt.Fprintf(os.Stderr, "endless-event: error: %v\n", err)
		os.Exit(1)
	}

	if _, err := currentDB.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS proj", tempPath)); err != nil {
		os.Remove(tempPath)
		fmt.Fprintf(os.Stderr, "endless-event: error attaching temp db: %v\n", err)
		os.Exit(1)
	}

	tx, err := currentDB.Begin()
	if err != nil {
		os.Remove(tempPath)
		fmt.Fprintf(os.Stderr, "endless-event: error: %v\n", err)
		os.Exit(1)
	}

	// Delete current tasks for this project and insert from projection
	if _, err := tx.Exec("DELETE FROM tasks WHERE project_id IN (SELECT id FROM projects WHERE name IN (SELECT name FROM proj.projects))"); err != nil {
		tx.Rollback()
		os.Remove(tempPath)
		fmt.Fprintf(os.Stderr, "endless-event: error clearing tasks: %v\n", err)
		os.Exit(1)
	}

	if _, err := tx.Exec("INSERT INTO tasks SELECT * FROM proj.tasks"); err != nil {
		tx.Rollback()
		os.Remove(tempPath)
		fmt.Fprintf(os.Stderr, "endless-event: error inserting projected tasks: %v\n", err)
		os.Exit(1)
	}

	if err := tx.Commit(); err != nil {
		os.Remove(tempPath)
		fmt.Fprintf(os.Stderr, "endless-event: error committing: %v\n", err)
		os.Exit(1)
	}

	currentDB.Exec("DETACH DATABASE proj")
	os.Remove(tempPath)
	fmt.Printf("Rebuilt: tasks table replaced with %d projected tasks.\n", projResult.TasksCreated)
}

func runReapWorktrees() {
	fs := flag.NewFlagSet("reap-worktrees", flag.ExitOnError)
	projectRoot := fs.String("project-root", "", "Project root directory")
	ttlOverride := fs.String("ttl", "", "TTL override (default: read from .endless/config.json, fallback 14d)")
	fs.Parse(os.Args[2:])

	if *projectRoot == "" {
		fmt.Fprintf(os.Stderr, "endless-event: reap-worktrees: --project-root is required\n")
		os.Exit(1)
	}

	ttlStr := *ttlOverride
	if ttlStr == "" {
		ttlStr = monitor.ReadWorktreeTTLConfig(*projectRoot)
	}
	ttl := monitor.DefaultWorktreeTTL
	if ttlStr != "" {
		parsed, err := monitor.ParseWorktreeTTL(ttlStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "endless-event: reap-worktrees: parse ttl %q: %v (using default %s)\n",
				ttlStr, err, monitor.DefaultWorktreeTTL)
		} else {
			ttl = parsed
		}
	}

	if err := monitor.ReapStaleWorktrees(*projectRoot, ttl); err != nil {
		fmt.Fprintf(os.Stderr, "endless-event: reap-worktrees: %v\n", err)
		os.Exit(1)
	}
}

// schemaVersionDDL matches the shape in internal/schema/schema.sql. Created
// defensively before checking/recording the applied marker.
const schemaVersionDDL = `CREATE TABLE IF NOT EXISTS _schema_version (
	name       TEXT PRIMARY KEY,
	applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
)`

// runApplyChange applies one per-ticket schema-change file
// (internal/schema/changes/<name>.{sql,go}) and records it in _schema_version.
// The change name is the file's basename without extension; the same name is
// used both as the applied marker and (for .go) by the runner helper. Already
// applied changes are skipped. Effects and the marker insert commit together.
func runApplyChange() {
	fs := flag.NewFlagSet("apply-change", flag.ExitOnError)
	fs.Parse(os.Args[2:])

	args := fs.Args()
	if len(args) != 1 {
		emitChangeErr("", "apply-change requires exactly one <path> argument")
	}
	path, err := filepath.Abs(args[0])
	if err != nil {
		emitChangeErr("", fmt.Sprintf("resolve path: %v", err))
	}
	if _, err = os.Stat(path); err != nil {
		emitChangeErr("", fmt.Sprintf("change file not found: %s", path))
	}

	ext := strings.ToLower(filepath.Ext(path))
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	db, err := monitor.DB()
	if err != nil {
		emitChangeErr(name, fmt.Sprintf("open db: %v", err))
	}
	if _, err = db.Exec(schemaVersionDDL); err != nil {
		emitChangeErr(name, fmt.Sprintf("ensure _schema_version: %v", err))
	}
	var applied int
	db.QueryRow("SELECT count(*) FROM _schema_version WHERE name = ?", name).Scan(&applied)
	if applied > 0 {
		emitChangeResult(name, "skipped", "already applied")
		return
	}

	switch ext {
	case ".sql":
		applySQLChange(db, path, name)
	case ".go":
		applyGoChange(path, name)
	default:
		emitChangeErr(name, fmt.Sprintf("unsupported change extension %q (only .sql and .go)", ext))
	}
}

// applySQLChange runs a .sql change file's statements and the marker insert in
// a single BEGIN IMMEDIATE transaction on the shared single connection. The
// file may itself reshape _schema_version (as the E-1459 reshape does); the
// marker insert runs after the file's statements, against whatever shape the
// file leaves behind.
func applySQLChange(db *sql.DB, path, name string) {
	content, err := os.ReadFile(path)
	if err != nil {
		emitChangeErr(name, fmt.Sprintf("read change file: %v", err))
	}
	if _, err = db.Exec("BEGIN IMMEDIATE TRANSACTION"); err != nil {
		emitChangeErr(name, fmt.Sprintf("begin: %v", err))
	}
	if _, err = db.Exec(string(content)); err != nil {
		db.Exec("ROLLBACK")
		emitChangeErr(name, fmt.Sprintf("apply: %v", err))
	}
	if _, err = db.Exec("INSERT INTO _schema_version (name) VALUES (?)", name); err != nil {
		db.Exec("ROLLBACK")
		emitChangeErr(name, fmt.Sprintf("record marker: %v", err))
	}
	if _, err = db.Exec("COMMIT"); err != nil {
		db.Exec("ROLLBACK")
		emitChangeErr(name, fmt.Sprintf("commit: %v", err))
	}
	emitChangeResult(name, "applied", "")
}

// applyGoChange runs a .go change via `go run`. The script uses the runner
// helper to do its own BEGIN IMMEDIATE + work + marker insert + COMMIT. The DB
// path is passed via ENDLESS_CHANGE_DB so the subprocess targets the exact same
// file this process resolved. The script's logs go to stderr; this process's
// stdout stays clean JSON. The runner's exit code is propagated on failure.
func applyGoChange(path, name string) {
	cmd := exec.Command("go", "run", path)
	cmd.Env = append(os.Environ(), "ENDLESS_CHANGE_DB="+monitor.DBPath())
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		emitChangeErr(name, fmt.Sprintf("go run %s: %v", path, err))
	}
	emitChangeResult(name, "applied", "")
}

func runBackup() {
	monitor.BackupDB()
	b, _ := json.Marshal(map[string]any{"status": "ok"})
	fmt.Println(string(b))
}

func emitChangeResult(name, status, reason string) {
	out := map[string]any{"name": name, "status": status}
	if reason != "" {
		out["reason"] = reason
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
}

func emitChangeErr(name, msg string) {
	out := map[string]any{"status": "error", "error": msg}
	if name != "" {
		out["name"] = name
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
	os.Exit(1)
}
