package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "emit" {
		fmt.Fprintf(os.Stderr, "Usage: endless-event emit [flags]\n")
		os.Exit(1)
	}

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

	// Build event
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

	// Validate envelope
	if err := evt.Validate(); err != nil {
		return err
	}

	// Execute SQL mutation
	result, err := events.Execute(&evt)
	if err != nil {
		return err
	}

	// Update entity ID if task was created (now we have the real ID)
	if result != nil && result.TaskID > 0 {
		evt.Entity.ID = fmt.Sprintf("%d", result.TaskID)
	}

	// Marshal to JSON
	line, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	// Write to segment file
	writer, err := events.NewWriter(projectRoot, nodeIDStr)
	if err != nil {
		return fmt.Errorf("create writer: %w", err)
	}
	if err := writer.Append(line); err != nil {
		return err
	}

	// Output result to stdout for Python to parse
	output := map[string]string{
		"ts":   ts.String(),
		"kind": kindStr,
	}
	if result != nil && result.TaskID > 0 {
		output["id"] = fmt.Sprintf("E-%d", result.TaskID)
	}
	outJSON, _ := json.Marshal(output)
	fmt.Println(string(outJSON))

	return nil
}
