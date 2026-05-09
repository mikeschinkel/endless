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
//
// E-1197 transition window: also reads from the legacy .endless/events/
// directory if it still exists (e.g., another tool wrote there before
// migration ran). The MigrateLegacyLedger() helper called from NewWriter
// normally sweeps this away on first write, but a read-before-write code
// path could still encounter it.
func ReadAllEvents(projectRoot string) ([]Event, error) {
	var allEvents []Event

	for _, sd := range []scanDir{
		{
			path:   filepath.Join(projectRoot, ".endless", LedgerDirName),
			prefix: LedgerFilePrefix,
		},
		{
			path:   filepath.Join(projectRoot, ".endless", LegacyDirName),
			prefix: LegacyFilePrefix,
		},
	} {
		events, err := readDir(sd)
		if err != nil {
			return nil, err
		}
		allEvents = append(allEvents, events...)
	}

	// Sort by kairos timestamp (lexicographic sort = causal sort)
	sort.Slice(allEvents, func(i, j int) bool {
		return allEvents[i].TS < allEvents[j].TS
	})

	return allEvents, nil
}

type scanDir struct {
	path   string
	prefix string
}

func readDir(sd scanDir) ([]Event, error) {
	entries, err := os.ReadDir(sd.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("events: read dir %s: %w", sd.path, err)
	}

	var events []Event
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, sd.prefix) || !strings.HasSuffix(name, LedgerFileSuffix) {
			continue
		}
		path := filepath.Join(sd.path, name)
		segEvents, err := readSegmentFile(path)
		if err != nil {
			return nil, fmt.Errorf("events: read segment %s: %w", name, err)
		}
		events = append(events, segEvents...)
	}
	return events, nil
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
