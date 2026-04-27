package events

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// MaxEventLineBytes is the maximum allowed size for a single JSONL event line.
// This is a sanity check against bugs (e.g., accidentally serializing a binary file),
// not a correctness requirement. Local filesystem O_APPEND writes are atomic regardless
// of size.
const MaxEventLineBytes = 1024 * 1024 // 1MB

// DefaultMaxEventsPerSegment is the default rotation threshold.
const DefaultMaxEventsPerSegment = 10000

// Writer appends events to segmented JSONL files.
// Each node writes to its own segments: events-{nodeHex}-{seq:06d}.jsonl
type Writer struct {
	eventsDir string
	nodeHex   string
	seq       int
	count     int
	maxCount  int
}

// NewWriter creates a Writer for the given project root and node.
// It scans existing segments to determine the current sequence and count.
func NewWriter(projectRoot string, nodeHex string) (*Writer, error) {
	eventsDir := filepath.Join(projectRoot, ".endless", "events")
	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		return nil, fmt.Errorf("events: create events dir: %w", err)
	}

	w := &Writer{
		eventsDir: eventsDir,
		nodeHex:   nodeHex,
		maxCount:  DefaultMaxEventsPerSegment,
	}

	seq, count, err := w.scanSegments()
	if err != nil {
		return nil, err
	}
	if seq == 0 {
		seq = 1 // start at 1 if no segments exist
	}
	w.seq = seq
	w.count = count
	return w, nil
}

// Append writes a JSONL line to the current segment file using O_APPEND.
// Returns an error if the line exceeds MaxEventLineBytes.
func (w *Writer) Append(line []byte) error {
	if len(line) > MaxEventLineBytes {
		return fmt.Errorf("events: JSONL line is %d bytes, exceeds %d byte limit (kind may contain oversized payload)",
			len(line), MaxEventLineBytes)
	}

	// Rotate if current segment is full
	if w.count >= w.maxCount {
		w.seq++
		w.count = 0
	}

	path := w.segmentPath(w.seq)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("events: open segment: %w", err)
	}
	defer f.Close()

	// Write line + newline
	buf := make([]byte, len(line)+1)
	copy(buf, line)
	buf[len(line)] = '\n'

	if _, err := f.Write(buf); err != nil {
		return fmt.Errorf("events: write segment: %w", err)
	}

	w.count++
	return nil
}

// SetMaxCount overrides the rotation threshold (for testing).
func (w *Writer) SetMaxCount(n int) { w.maxCount = n }

// CurrentSegment returns the filename of the current segment.
func (w *Writer) CurrentSegment() string {
	return w.segmentName(w.seq)
}

func (w *Writer) segmentName(seq int) string {
	return fmt.Sprintf("events-%s-%06d.jsonl", w.nodeHex, seq)
}

func (w *Writer) segmentPath(seq int) string {
	return filepath.Join(w.eventsDir, w.segmentName(seq))
}

// scanSegments finds the highest sequence number and event count for this node.
func (w *Writer) scanSegments() (seq int, count int, err error) {
	prefix := fmt.Sprintf("events-%s-", w.nodeHex)

	entries, err := os.ReadDir(w.eventsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("events: read events dir: %w", err)
	}

	var seqs []int
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		// Extract sequence number: events-{node}-{seq}.jsonl
		seqStr := strings.TrimPrefix(name, prefix)
		seqStr = strings.TrimSuffix(seqStr, ".jsonl")
		s, err := strconv.Atoi(seqStr)
		if err != nil {
			continue
		}
		seqs = append(seqs, s)
	}

	if len(seqs) == 0 {
		return 0, 0, nil
	}

	sort.Ints(seqs)
	highestSeq := seqs[len(seqs)-1]

	// Count lines in the highest segment
	data, err := os.ReadFile(w.segmentPath(highestSeq))
	if err != nil {
		return highestSeq, 0, nil
	}
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}

	return highestSeq, lines, nil
}
