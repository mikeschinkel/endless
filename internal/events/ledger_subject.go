package events

// Ledger commit subjects that name their events (E-2498).
//
// A ledger commit used to be titled LegacyLedgerCommitSubject whatever it held,
// so `git log` on any project Endless manages was a wall of identical lines.
// Each one now names the events it records, one line per event in the form
// `<kind> <entity>[ <value>]`:
//
//	Endless: task.status_changed E-2421 → unverified
//
// Consecutive ledger commits still fold into one by amending HEAD. A folded
// commit's subject names its first event plus a count of the rest, and its body
// lists every event:
//
//	Endless: task.created E-2510 Fix the l… +2 more
//
//	task.created E-2510 Fix the ledger subject
//	task_dep.created E-2510 cleans_up E-2498
//	epic.status_derived E-2400 → underway
//
// The message is rebuilt from the commit's own ledger diff on every amend, so
// it describes what the commit holds rather than a count kept on the side.
//
// Subjects are no longer a fixed string, so nothing may recognise a ledger
// commit by comparing subjects. IsLedgerCommit is the predicate; Python's
// worktree_cmd._is_ledger_commit mirrors it for land's orphan-drop.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// LegacyLedgerCommitSubject is the subject every ledger commit carried before
// E-2498. History is full of them, so IsLedgerSubject still accepts it, and a
// commit whose events cannot be read still falls back to it.
const LegacyLedgerCommitSubject = "Endless: record ledger entry"

// ledgerSubjectLimit caps a ledger commit's subject, in runes.
const ledgerSubjectLimit = 60

// ledgerBodyLineLimit caps each event line in a folded commit's body, in runes.
const ledgerBodyLineLimit = 72

// ledgerSubjectRE matches the subjects LedgerCommitMessage writes: the
// auto-commit prefix, then an `<entity>.<verb>` kind. No other Endless
// auto-commit has a dotted word there. Land's `Endless: auto-record session
// activity` can hold nothing but ledger lines, and must not read as amendable.
var ledgerSubjectRE = regexp.MustCompile(`^Endless: [a-z_]+\.[a-z_]+( |$)`)

// IsLedgerSubject reports whether subject is one a ledger commit carries.
// Necessary but not sufficient: IsLedgerCommit also checks the commit's paths.
func IsLedgerSubject(subject string) bool {
	subject = strings.TrimSpace(subject)
	return subject == LegacyLedgerCommitSubject || ledgerSubjectRE.MatchString(subject)
}

// IsLedgerCommit reports whether rev is a ledger auto-commit: its subject is a
// ledger subject and it touches nothing outside .endless/db-ledger/*.jsonl. A
// commit that touches no path at all is not one.
func IsLedgerCommit(projectRoot, rev, subject string) (bool, error) {
	if !IsLedgerSubject(subject) {
		return false, nil
	}
	out, err := runGitOutput(projectRoot,
		"diff-tree", "--root", "-r", "--no-commit-id", "--name-only", rev,
	)
	if err != nil {
		return false, fmt.Errorf("list paths at %s: %w", rev, err)
	}
	paths := 0
	for _, p := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !isLedgerSegmentPath(p) {
			return false, nil
		}
		paths++
	}
	return paths > 0, nil
}

// isLedgerSegmentPath reports whether a repo-relative path is a ledger segment.
func isLedgerSegmentPath(p string) bool {
	return strings.HasPrefix(p, ledgerDirRel+"/") && strings.HasSuffix(p, ".jsonl")
}

// ledgerMessage builds the message for the ledger commit about to be made: the
// events HEAD already holds when amending, then the events now staged in paths.
func ledgerMessage(projectRoot string, paths []string, amend bool) (string, error) {
	evts, err := ledgerEvents(projectRoot, paths, amend)
	if err != nil {
		return "", err
	}
	return LedgerCommitMessage(evts), nil
}

// HoldsCorrection reports whether any of the events is a correction entry.
// A commit holding one is never folded with another (E-2486): its subject
// names the correction, and folding would bury it under `+N more`.
func HoldsCorrection(evts []Event) bool {
	for _, e := range evts {
		if e.Kind == KindLedgerCorrected {
			return true
		}
	}
	return false
}

// ledgerEvents lists the events the ledger commit about to be made would hold:
// the events HEAD already holds when amending, then the events now staged in
// paths.
func ledgerEvents(projectRoot string, paths []string, amend bool) ([]Event, error) {
	var evts []Event
	if amend {
		out, err := runGitOutput(projectRoot,
			"diff-tree", "--root", "-r", "-p", "--no-commit-id", "HEAD",
			"--", ledgerDirRel,
		)
		if err != nil {
			return nil, fmt.Errorf("read HEAD's ledger events: %w", err)
		}
		evts = append(evts, addedEvents(out)...)
	}
	// With no HEAD, `diff --cached` compares against the empty tree.
	out, err := runGitOutput(projectRoot,
		append([]string{"diff", "--cached", "--"}, paths...)...)
	if err != nil {
		return nil, fmt.Errorf("read staged ledger events: %w", err)
	}
	evts = append(evts, addedEvents(out)...)
	return evts, nil
}

// addedEvents parses the events a unified diff adds. Only the fields a message
// uses are decoded, so an envelope change elsewhere cannot blank a subject. A
// line that is not an event is skipped: the message describes the commit, and
// is never the reason a commit fails.
func addedEvents(diff string) []Event {
	var evts []Event
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "+{") {
			continue
		}
		var e struct {
			Kind    Kind            `json:"kind"`
			Entity  EntityRef       `json:"entity"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(line[1:]), &e) != nil || e.Kind == "" {
			continue
		}
		evts = append(evts, Event{Kind: e.Kind, Entity: e.Entity, Payload: e.Payload})
	}
	return evts
}

// LedgerCommitMessage is the full commit message for a ledger commit holding
// evts, in order. Several events get a `+N more` subject and a body listing
// each. No events falls back to LegacyLedgerCommitSubject.
//
// The subject it returns always satisfies IsLedgerSubject, since that is what
// lets the next event fold in. A first kind that is not `<entity>.<verb>` —
// none is today — would not, so that commit keeps the legacy subject and lists
// its events in the body.
func LedgerCommitMessage(evts []Event) string {
	if len(evts) == 0 {
		return LegacyLedgerCommitSubject
	}
	suffix := ""
	if len(evts) > 1 {
		suffix = fmt.Sprintf(" +%d more", len(evts)-1)
	}
	room := ledgerSubjectLimit - runeLen(AutoCommitSubjectPrefix) - runeLen(suffix)
	// A correction leads its commit (E-2486): its change entries precede it in
	// the segment, but `git log` should say that a correction happened, not
	// which row moved first. The body still lists every event in order.
	lead := evts[0]
	for _, e := range evts {
		if e.Kind == KindLedgerCorrected {
			lead = e
			break
		}
	}
	msg := AutoCommitSubjectPrefix + describeEvent(lead).fit(room) + suffix
	if !IsLedgerSubject(msg) {
		msg = LegacyLedgerCommitSubject
	} else if len(evts) == 1 {
		return msg
	}
	lines := make([]string, len(evts))
	for i, e := range evts {
		lines[i] = describeEvent(e).fit(ledgerBodyLineLimit)
	}
	return msg + "\n\n" + strings.Join(lines, "\n")
}

// eventLine is one event as `<kind> <entity>[ <value>]`, kept in parts so
// fit can shorten the right one.
type eventLine struct {
	kind   string
	entity string
	value  string
}

func (l eventLine) String() string {
	s := l.kind
	if l.entity != "" {
		s += " " + l.entity
	}
	if l.value != "" {
		s += " " + l.value
	}
	return s
}

// fit shortens the line to at most room runes. The value goes first, cut from
// its end. When kind and entity alone are too long the entity is cut from its
// start, because the end of a path names the file.
func (l eventLine) fit(room int) string {
	s := l.String()
	if runeLen(s) <= room {
		return s
	}
	head := l.kind + " " + l.entity
	if l.entity == "" {
		head = l.kind
	}
	if runeLen(head)+2 <= room && l.value != "" {
		return truncateEnd(s, room)
	}
	if l.entity == "" || runeLen(l.kind)+2 >= room {
		return truncateEnd(head, room)
	}
	return l.kind + " " + truncateStart(l.entity, room-runeLen(l.kind)-1)
}

// eventPayload holds every payload field a subject value is drawn from. Kinds
// share field names, so one shape serves them all.
type eventPayload struct {
	NewStatus        string                     `json:"new_status"`
	Status           string                     `json:"status"`
	Title            string                     `json:"title"`
	Fields           map[string]json.RawMessage `json:"fields"`
	SourceID         int64                      `json:"source_id"`
	TargetID         int64                      `json:"target_id"`
	DepType          string                     `json:"dep_type"`
	SourceDecisionID int64                      `json:"source_decision_id"`
	TargetKind       string                     `json:"target_kind"`
	RelationType     string                     `json:"relation_type"`
	MergeCommitSHA   string                     `json:"merge_commit_sha"`
	SessionID        int64                      `json:"session_id"`
	Target           string                     `json:"target"`
	Key              string                     `json:"key"`
	Op               string                     `json:"op"`
	Values           map[string]json.RawMessage `json:"values"`
	Reason           string                     `json:"reason"`
	Ops              []struct {
		Op  string `json:"op"`
		Seq int64  `json:"seq"`
	} `json:"ops"`
}

// describeEvent names one event: its kind, its entity by display id, and for
// the kinds that have one, the value it set.
func describeEvent(e Event) eventLine {
	l := eventLine{kind: string(e.Kind), entity: entityLabel(e.Entity)}
	var p eventPayload
	// A payload that does not parse still names the kind and entity.
	_ = json.Unmarshal(e.Payload, &p)

	switch e.Kind {
	case KindTaskStatusChanged, KindEpicStatusDerived:
		l.value = arrow(p.NewStatus)
	case KindTaskQuestionResolved:
		l.value = arrow(p.Status)
	case KindTaskCreated, KindDecisionCreated:
		l.value = p.Title
	case KindTaskFieldsUpdated, KindDecisionFieldsUpdated:
		names := make([]string, 0, len(p.Fields))
		for name := range p.Fields {
			names = append(names, name)
		}
		sort.Strings(names)
		l.value = strings.Join(names, ", ")
	case KindTaskDepCreated, KindTaskDepDeleted:
		l.entity = fmt.Sprintf("E-%d", p.SourceID)
		l.value = fmt.Sprintf("%s E-%d", p.DepType, p.TargetID)
	case KindDecisionRelationCreated, KindDecisionRelationDeleted:
		l.entity = fmt.Sprintf("ED-%d", p.SourceDecisionID)
		l.value = fmt.Sprintf("%s %s", p.RelationType,
			entityLabel(EntityRef{Type: EntityType(p.TargetKind), ID: fmt.Sprint(p.TargetID)}))
	case KindTaskPlanItemsChanged:
		ops := make([]string, len(p.Ops))
		for i, op := range p.Ops {
			ops[i] = fmt.Sprintf("%s P%d", op.Op, op.Seq)
		}
		l.value = strings.Join(ops, ", ")
	case KindTaskLanded:
		l.value = p.MergeCommitSHA
		if len(l.value) > 9 {
			l.value = l.value[:9]
		}
	case KindTaskClaimed, KindTaskBound:
		if p.SessionID != 0 {
			l.value = fmt.Sprintf("ES-%d", p.SessionID)
		}
	case KindLedgerRowChanged:
		// `update task 2486 status`: the operation, the row, the columns.
		names := make([]string, 0, len(p.Values))
		for name := range p.Values {
			names = append(names, name)
		}
		sort.Strings(names)
		l.value = strings.TrimSpace(fmt.Sprintf("%s %s %s %s", p.Op, p.Target, p.Key, strings.Join(names, ", ")))
	case KindLedgerCorrected:
		l.value = p.Reason
	}
	l.value = strings.Join(strings.Fields(l.value), " ")
	return l
}

// entityLabel renders an entity by the id a person types: E-, ED-, EQ-, ES-.
// Any other entity shows its raw id (for shipped_file, its path).
func entityLabel(ref EntityRef) string {
	switch ref.Type {
	case EntityTask:
		return "E-" + ref.ID
	case EntityDecision:
		return "ED-" + ref.ID
	case EntityTaskQuestion:
		return "EQ-" + ref.ID
	case EntitySession:
		return "ES-" + ref.ID
	}
	return ref.ID
}

func arrow(v string) string {
	if v == "" {
		return ""
	}
	return "→ " + v
}

func runeLen(s string) int { return len([]rune(s)) }

// truncateEnd cuts s to n runes, ending in an ellipsis when it cut anything.
func truncateEnd(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return strings.TrimRight(string(r[:n-1]), " ") + "…"
}

// truncateStart cuts s to n runes from the front, starting with an ellipsis.
func truncateStart(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return "…" + string(r[len(r)-n+1:])
}
