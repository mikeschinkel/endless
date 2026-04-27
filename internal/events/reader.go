package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ReadAllEvents reads all event segment files from a project's .endless/events/ directory,
// parses each JSONL line, and returns events sorted by kairos timestamp.
func ReadAllEvents(projectRoot string) ([]Event, error) {
	eventsDir := filepath.Join(projectRoot, ".endless", "events")

	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("events: read events dir: %w", err)
	}

	var allEvents []Event

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		path := filepath.Join(eventsDir, entry.Name())
		events, err := readSegmentFile(path)
		if err != nil {
			return nil, fmt.Errorf("events: read segment %s: %w", entry.Name(), err)
		}
		allEvents = append(allEvents, events...)
	}

	// Sort by kairos timestamp (lexicographic sort = causal sort)
	sort.Slice(allEvents, func(i, j int) bool {
		return allEvents[i].TS < allEvents[j].TS
	})

	return allEvents, nil
}

func readSegmentFile(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, MaxEventLineBytes+1024), MaxEventLineBytes+1024)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var evt Event
		if err := json.Unmarshal(line, &evt); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}
		events = append(events, evt)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return events, nil
}
