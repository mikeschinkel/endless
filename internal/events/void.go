package events

// The voiding entry (E-2578).
//
// An append-only ledger cannot take a line back. When lines are appended and
// the database COMMIT then fails, the ledger says something happened that did
// not, and the only honest repair is another line that says so: a
// ledger.voided entry naming the segment and the byte offset of every line in
// the run, and the error that stopped the apply. The projector reads these
// before it replays and skips every line they name.
//
// The entry is written with no database access — the database is what just
// failed — under the ledger lock the run still holds, so it is adjacent to the
// lines it voids. It names its segment explicitly because a voiding written by
// hand can land in a later segment than the lines it names.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// VoidedPayload names the lines that never took effect.
type VoidedPayload struct {
	// Segment is the file the voided lines are in, by name.
	Segment string `json:"segment"`
	// Refs is the byte offset of every voided line in that segment, the form
	// ledger.corrected uses for its change entries.
	Refs []int64 `json:"refs"`
	// Error is what stopped the apply, when a program observed it.
	Error string `json:"error,omitempty"`
	// Reason is why the lines are voided, in words: emit's fixed sentence, or
	// what the user gave `event void`.
	Reason string `json:"reason,omitempty"`
}

// VoidedError reports an apply that was voided: its lines reached the ledger,
// the database COMMIT then failed, and a ledger.voided line follows them.
type VoidedError struct {
	Segment    string  // the segment the run and its voiding are in
	Refs       []int64 // the voided lines
	VoidOffset int64   // where the voiding line starts
	Cause      error   // what stopped the apply
}

func (e *VoidedError) Error() string {
	return fmt.Sprintf("%s; the ledger holds the entry (%s, offsets %v) and its voiding at offset %d, and the database is unchanged",
		e.Cause, e.Segment, e.Refs, e.VoidOffset)
}

func (e *VoidedError) Unwrap() error { return e.Cause }

// voidReasonCommitFailed is the Reason emit writes on the voiding it leaves.
const voidReasonCommitFailed = "the database COMMIT failed after the ledger append; the database is unchanged and the command was not recorded"

// voidRun appends the ledger.voided line for a run whose COMMIT failed, in the
// run the writer still has open, and returns the refusal the emit ends with.
// The voiding carries the voided event's actor: it records whose command did
// not take effect, and no one else acted.
func voidRun(w *Writer, clock *kairos.Clock, voided *Event, segment string, refs []int64, cause error) error {
	payload, err := json.Marshal(VoidedPayload{
		Segment: segment,
		Refs:    refs,
		Error:   cause.Error(),
		Reason:  voidReasonCommitFailed,
	})
	if err != nil {
		return unvoidedRefusal(segment, refs, cause, fmt.Errorf("marshal ledger.voided payload: %w", err))
	}
	evt := &Event{
		V:       Version,
		TS:      clock.Now().String(),
		Kind:    KindLedgerVoided,
		Project: voided.Project,
		Entity:  voided.Entity,
		Actor:   voided.Actor,
		Payload: payload,
	}
	if err := evt.Validate(); err != nil {
		return unvoidedRefusal(segment, refs, cause, err)
	}
	line, err := json.Marshal(evt)
	if err != nil {
		return unvoidedRefusal(segment, refs, cause, fmt.Errorf("marshal ledger.voided event: %w", err))
	}
	offset, err := w.AppendOffset(line)
	if err != nil {
		return unvoidedRefusal(segment, refs, cause, err)
	}
	ve := &VoidedError{Segment: segment, Refs: refs, VoidOffset: offset, Cause: cause}
	// REPORT: a COMMIT that fails after its SQL ran is the database refusing a
	// write, which the user has to look at before anything is retried. The
	// summary says what the ledger now holds, so nobody reads "nothing was
	// recorded" into a non-zero exit.
	return refusal.Report(
		ve.Error(),
		"whether the database is healthy enough to re-run the command: its COMMIT failed after the SQL ran, and the ledger records the attempt as voided",
	).Cause(ve)
}

// unvoidedRefusal is the failure that leaves lines on the ledger with nothing
// saying they never took effect: the COMMIT failed and the voiding could not
// be written either. The only repair is by hand, and the summary names it.
func unvoidedRefusal(segment string, refs []int64, cause, voidErr error) error {
	return refusal.Report(
		fmt.Sprintf("%s, and the voiding line could not be written (%s): the ledger holds lines at %s offsets %v that never took effect",
			cause, voidErr, segment, refs),
		fmt.Sprintf("how to void the lines by hand: `endless-go event void %s%s --reason <text>` once the ledger can be written",
			segment, offsetFlags(refs)),
	).Cause(cause)
}

// offsetFlags renders refs as the --offset flags `event void` takes.
func offsetFlags(refs []int64) string {
	s := ""
	for _, r := range refs {
		s += fmt.Sprintf(" --offset %d", r)
	}
	return s
}

// LedgerLine is one line read back from a segment by offset.
type LedgerLine struct {
	Offset int64
	Event  Event
}

// ReadLinesAt reads the lines that start at the given byte offsets of one
// segment under ledgerRoot's ledger directory. It refuses an offset that is
// not the start of a line, or a line that is not an event: a voiding names
// lines, and a wrong offset would void nothing while claiming to. The lines
// come back sorted by offset with duplicates dropped, each carrying its
// Segment and Offset.
func ReadLinesAt(ledgerRoot, segment string, offsets []int64) ([]LedgerLine, error) {
	if filepath.Base(segment) != segment {
		return nil, refusal.NoReport(
			fmt.Sprintf("events: %q is a path, not a segment name", segment),
			"Pass the segment's file name alone, such as db-entries-a7f3-000029.jsonl")
	}
	if len(offsets) == 0 {
		return nil, refusal.NoReport("events: no offsets given", "Pass --offset <n> for each line to void")
	}
	path := filepath.Join(ledgerRoot, ".endless", LedgerDirName, segment)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, refusal.NoReport(
				fmt.Sprintf("events: no segment %s in %s", segment, filepath.Dir(path)),
				"Pass the name of a segment that exists in the ledger directory")
		}
		return nil, fmt.Errorf("events: read segment %s: %w", segment, err)
	}

	sorted := append([]int64(nil), offsets...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var lines []LedgerLine
	for i, off := range sorted {
		if i > 0 && off == sorted[i-1] {
			continue
		}
		if off < 0 || off >= int64(len(data)) || (off > 0 && data[off-1] != '\n') {
			return nil, refusal.NoReport(
				fmt.Sprintf("events: offset %d does not start a line in %s", off, segment),
				"Pass the byte offset at which the line begins; a ledger commit's message or `grep -b` gives it")
		}
		end := off
		for end < int64(len(data)) && data[end] != '\n' {
			end++
		}
		var evt Event
		if err := json.Unmarshal(data[off:end], &evt); err != nil || evt.Kind == "" {
			return nil, refusal.NoReport(
				fmt.Sprintf("events: the line at offset %d in %s is not an event", off, segment),
				"Pass the offset of an event line")
		}
		evt.Segment = segment
		evt.Offset = off
		lines = append(lines, LedgerLine{Offset: off, Event: evt})
	}
	return lines, nil
}

// NewVoiding builds the ledger.voided event `event void` appends for lines a
// person named: the segment and offsets of those lines, the reason given, the
// first line's entity, and the person as actor.
func NewVoiding(clock *kairos.Clock, project string, actor Actor, lines []LedgerLine, reason string) (*Event, error) {
	if len(lines) == 0 {
		return nil, fmt.Errorf("events: a voiding needs at least one line")
	}
	refs := make([]int64, len(lines))
	for i, l := range lines {
		if l.Event.Kind == KindLedgerVoided {
			return nil, refusal.NoReport(
				fmt.Sprintf("events: the line at offset %d is itself a voiding, which is not voided", l.Offset),
				"A voiding stands; to annotate it, use `endless correct <target> annotate`")
		}
		refs[i] = l.Offset
	}
	payload, err := json.Marshal(VoidedPayload{
		Segment: lines[0].Event.Segment,
		Refs:    refs,
		Reason:  reason,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal ledger.voided payload: %w", err)
	}
	evt := &Event{
		V:       Version,
		TS:      clock.Now().String(),
		Kind:    KindLedgerVoided,
		Project: project,
		Entity:  lines[0].Event.Entity,
		Actor:   actor,
		Payload: payload,
	}
	if err := evt.Validate(); err != nil {
		return nil, err
	}
	return evt, nil
}

// lineRef identifies one ledger line by where it sits.
type lineRef struct {
	segment string
	offset  int64
}

// voidedLines collects every line the ledger.voided entries among evts name.
func voidedLines(evts []Event) map[lineRef]bool {
	voided := map[lineRef]bool{}
	for _, e := range evts {
		if e.Kind != KindLedgerVoided {
			continue
		}
		var p VoidedPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			continue
		}
		for _, off := range p.Refs {
			voided[lineRef{p.Segment, off}] = true
		}
	}
	return voided
}

// readSegmentFile reads one segment's events, each stamped with the segment's
// name and the byte offset its line starts at. Offsets count every byte of the
// file, blank lines included, so they match what the writer reported when it
// appended the line.
func readSegmentFile(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	segment := filepath.Base(path)
	var events []Event
	r := bufio.NewReaderSize(f, 64*1024)
	var offset int64
	lineNum := 0
	for {
		line, err := r.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			break
		}
		lineNum++
		start := offset
		offset += int64(len(line))
		body := line
		if n := len(body); n > 0 && body[n-1] == '\n' {
			body = body[:n-1]
		}
		if len(body) > MaxEventLineBytes {
			return nil, fmt.Errorf("line %d: %d bytes exceeds the %d byte limit", lineNum, len(body), MaxEventLineBytes)
		}
		if len(body) > 0 {
			var evt Event
			if uerr := json.Unmarshal(body, &evt); uerr != nil {
				return nil, fmt.Errorf("line %d: %w", lineNum, uerr)
			}
			evt.Segment = segment
			evt.Offset = start
			events = append(events, evt)
		}
		if err != nil {
			break
		}
	}
	return events, nil
}
