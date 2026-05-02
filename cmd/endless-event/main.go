package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/monitor"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: endless-event <command> [flags]\n")
		fmt.Fprintf(os.Stderr, "Commands: emit, validate-db, rebuild-db, migrate-db\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "emit":
		runEmit()
	case "validate-db":
		runValidateDB()
	case "rebuild-db":
		runRebuildDB()
	case "migrate-db":
		runMigrateDB()
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
	nodeID := fs.String("node-id", "", "Kairos node ID (4-char hex)")
	projectRoot := fs.String("project-root", "", "Project root directory (for .endless/events/)")
	payload := fs.String("payload", "{}", "Event payload as JSON")
	correlationID := fs.String("cid", "", "Correlation ID (optional)")

	fs.Parse(os.Args[2:])

	if err := run(*kind, *project, *entityType, *entityID, *actorKind, *actorID,
		*nodeID, *projectRoot, *payload, *correlationID); err != nil {
		fmt.Fprintf(os.Stderr, "endless-event: error: %v\n", err)
		os.Exit(1)
	}
}

func run(kindStr, project, entityTypeStr, entityID, actorKindStr, actorID,
	nodeIDStr, projectRoot, payloadStr, correlationID string) error {

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
				Kind: events.ActorKind(actorKindStr),
				ID:   actorID,
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
				Kind: events.ActorKind(actorKindStr),
				ID:   actorID,
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

		// Execute SQL mutation (side effect of the event)
		if _, err := events.Execute(&evt); err != nil {
			return err
		}

		output := map[string]string{
			"ts":   ts.String(),
			"kind": kindStr,
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

func runMigrateDB() {
	fs := flag.NewFlagSet("migrate-db", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Report pending migrations without applying")
	forceRebuild := fs.Bool("force-rebuild", false, "Allow migrations marked RequiresRebuild")
	target := fs.Int("target", 0, "Highest version to apply (0 = current)")
	fs.Parse(os.Args[2:])

	runner := monitor.RunnerExplicit
	if *forceRebuild {
		runner = monitor.RunnerForceRebuild
	}

	result, err := monitor.Migrate(monitor.MigrateOpts{
		Runner:       runner,
		AllowRebuild: *forceRebuild,
		DryRun:       *dryRun,
		Target:       *target,
	})
	if err != nil {
		out := map[string]any{
			"applied": result.Applied,
			"skipped": result.Skipped,
			"error":   err.Error(),
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
		os.Exit(1)
	}

	b, _ := json.Marshal(result)
	fmt.Println(string(b))
}
