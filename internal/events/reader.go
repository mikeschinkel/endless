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

// ReadAllEvents reads all event segment files from a project's ledger
// directory, parses each JSONL line, and returns events sorted by kairos
// timestamp.
func ReadAllEvents(projectRoot string) ([]Event, error) {
	ledgerDir := filepath.Join(projectRoot, ".endless", LedgerDirName)

	entries, err := os.ReadDir(ledgerDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("events: read ledger dir: %w", err)
	}

	var allEvents []Event
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, LedgerFilePrefix) || !strings.HasSuffix(name, LedgerFileSuffix) {
			continue
		}
		path := filepath.Join(ledgerDir, name)
		segEvents, err := readSegmentFile(path)
		if err != nil {
			return nil, fmt.Errorf("events: read segment %s: %w", name, err)
		}
		allEvents = append(allEvents, segEvents...)
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
