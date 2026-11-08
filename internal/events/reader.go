package events

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ReadAllEvents reads all event segment files from a project's ledger
// directory, parses each JSONL line, and returns events sorted by kairos
// timestamp. Each event carries the segment it came from and the byte offset
// its line starts at (E-2578), which is how a ledger.voided entry names it.
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
