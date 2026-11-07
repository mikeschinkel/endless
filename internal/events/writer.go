package events

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
)

// MaxEventLineBytes is the maximum allowed size for a single JSONL event line.
// This is a sanity check against bugs (e.g., accidentally serializing a binary file),
// not a correctness requirement. Local filesystem O_APPEND writes are atomic regardless
// of size.
const MaxEventLineBytes = 1024 * 1024 // 1MB

// DefaultMaxEventsPerSegment is the default rotation threshold. Sized so
// each segment stays IDE-loadable when a user needs to open the ledger
// by hand: at ~1.1KB per JSONL entry, 500 entries yields ~550KB
// segments, comfortably under the 1-2MB threshold where JetBrains
// (and many other editors) start to lag.
const DefaultMaxEventsPerSegment = 500

// Path/naming constants for the durable event ledger (write-ahead record for
// the Endless SQLite DB). See decision E-1198 / task E-1197 for why these are
// "ledger" + "entries" and not "events" / "log" / "journal".
const (
	LedgerDirName    = "db-ledger"
	LedgerFilePrefix = "db-entries-"
	LedgerFileSuffix = ".jsonl"
)

// Writer appends events to segmented JSONL files.
// Each node writes to its own segments: db-entries-{nodeHex}-{seq:06d}.jsonl
type Writer struct {
	eventsDir string
	nodeHex   string
	seq       int
	count     int
	maxCount  int

	// lockFD is the open ledger lock while this writer holds it (E-2486):
	// for the length of one Append, or from BeginRun to EndRun.
	lockFD *os.File

	// inRun is set between BeginRun and EndRun. A run rotates once, before its
	// first line, so every line it appends lands in one segment; runNext is the
	// byte offset the next line of the run starts at.
	inRun   bool
	runNext int64
}

// NewWriter creates a Writer for the given project root and node.
// It scans existing segments to determine the current sequence and count.
func NewWriter(projectRoot string, nodeHex string) (*Writer, error) {
	eventsDir := filepath.Join(projectRoot, ".endless", LedgerDirName)
	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		return nil, fmt.Errorf("events: create ledger dir: %w", err)
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
//
// The write happens under the ledger lock (E-2486): taken for this one append,
// or already held by the run this writer is in.
func (w *Writer) Append(line []byte) error {
	_, err := w.AppendOffset(line)
	return err
}

// AppendOffset is Append reporting where the line starts: the byte offset of
// its first character in the segment file CurrentSegment names after the call.
// A correction entry records its change entries by this offset (E-2486).
func (w *Writer) AppendOffset(line []byte) (offset int64, err error) {
	if len(line) > MaxEventLineBytes {
		// Checked before the append, so nothing is on disk and the create lock is
		// rolled back — which is what makes this one retryable at all.
		return 0, refusal.NoReport(
			fmt.Sprintf("events: JSONL line is %d bytes, exceeds %d byte limit (kind may contain oversized payload)",
				len(line), MaxEventLineBytes),
			"Shrink the oversized field — long plan or analysis content belongs outside the event — and retry")
	}

	if !w.inRun {
		if err = w.acquire(); err != nil {
			return 0, err
		}
		defer w.release()
		w.rotateIfFull()
	}

	path := w.segmentPath(w.seq)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return 0, fmt.Errorf("events: open segment: %w", err)
	}
	defer f.Close()

	if w.inRun {
		offset = w.runNext
	} else {
		info, serr := f.Stat()
		if serr != nil {
			return 0, fmt.Errorf("events: stat segment: %w", serr)
		}
		offset = info.Size()
	}

	// Write line + newline
	buf := make([]byte, len(line)+1)
	copy(buf, line)
	buf[len(line)] = '\n'

	if _, err := f.Write(buf); err != nil {
		return 0, fmt.Errorf("events: write segment: %w", err)
	}

	w.count++
	if w.inRun {
		w.runNext = offset + int64(len(buf))
	}
	return offset, nil
}

// rotateIfFull moves to the next segment when the current one has reached the
// rotation threshold. Called under the lock.
func (w *Writer) rotateIfFull() {
	if w.count >= w.maxCount {
		w.seq++
		w.count = 0
	}
}

// BeginRun takes the ledger lock and holds it until EndRun, so every line
// appended in between is adjacent in one segment with nothing from another
// process among them (E-2486). The rotation limit is checked once, here, so a
// run never straddles two segments; a segment may go a few lines over its
// limit, which is harmless. The first offset comes from the segment's size, and
// each later one adds the previous line's length, newline included.
func (w *Writer) BeginRun() error {
	if w.inRun {
		return nil
	}
	if err := w.acquire(); err != nil {
		return err
	}
	w.rotateIfFull()
	info, err := os.Stat(w.segmentPath(w.seq))
	switch {
	case err == nil:
		w.runNext = info.Size()
	case os.IsNotExist(err):
		w.runNext = 0
	default:
		w.release()
		return fmt.Errorf("events: stat segment: %w", err)
	}
	w.inRun = true
	return nil
}

// EndRun releases the lock BeginRun took. Safe to call without a run open.
func (w *Writer) EndRun() {
	w.inRun = false
	w.release()
}

// SetMaxCount overrides the rotation threshold (for testing).
func (w *Writer) SetMaxCount(n int) { w.maxCount = n }

// CurrentSegment returns the filename of the current segment.
func (w *Writer) CurrentSegment() string {
	return w.segmentName(w.seq)
}

func (w *Writer) segmentName(seq int) string {
	return fmt.Sprintf("%s%s-%06d%s", LedgerFilePrefix, w.nodeHex, seq, LedgerFileSuffix)
}

func (w *Writer) segmentPath(seq int) string {
	return filepath.Join(w.eventsDir, w.segmentName(seq))
}

// scanSegments finds the highest sequence number and event count for this node.
func (w *Writer) scanSegments() (seq int, count int, err error) {
	prefix := fmt.Sprintf("%s%s-", LedgerFilePrefix, w.nodeHex)

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
		// Extract sequence number: db-entries-{node}-{seq}.jsonl
		seqStr := strings.TrimPrefix(name, prefix)
		seqStr = strings.TrimSuffix(seqStr, LedgerFileSuffix)
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
