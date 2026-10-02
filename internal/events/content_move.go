package events

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/taskcontent"
)

// Moving a `## Heading` section out of one task content into a content of its
// own (E-1562).
//
// Before a content kind existed, its text was written into another one under a
// heading — E-1544 filed every research justification into notes under
// `## Justification`. Declaring the kind is one constant; moving the text that
// already exists is this. It is planned here and EMITTED as ordinary
// task.fields_updated events by the caller, so the move is ledger-native: a
// rebuild replays it like any other change instead of re-running a script, and
// the live apply and the rebuild agree because both route the payload through
// taskContentFields.
//
// Generic over source, target and heading on purpose: E-1992's remaining slots
// each need the same move.

// SectionMove is one task's planned move: the payload fields to emit, and the
// timestamp to stamp the event with.
type SectionMove struct {
	TaskID int64 `json:"task_id"`

	// TS is the RFC3339 instant to stamp the event with, or "" for now.
	//
	// It is one microsecond after the last ledger event that wrote the source
	// content, so the history reads as when the moved text was written. Not
	// the event that FIRST wrote the section: the ledger replays in timestamp
	// order, and a move stamped before a later rewrite of the source would be
	// undone by that rewrite on every rebuild — the live database and the
	// rebuilt one would disagree. After the last write, nothing later touches
	// the source, so the two agree. "" when the ledger holds no such write
	// (content that predates the ledger): then now is the only safe instant.
	TS string `json:"ts"`

	// Fields is the task.fields_updated payload: the target content, and the
	// source with the section removed — "" when the section was all it held,
	// which the executor applies as a delete.
	Fields map[string]string `json:"fields"`
}

// SectionSkip is a task whose source holds the section but that is not moved,
// and why. Reported rather than guessed at: each one is a human's call.
type SectionSkip struct {
	TaskID int64  `json:"task_id"`
	Reason string `json:"reason"`
}

// SectionMovePlan is everything PlanSectionMoves found.
type SectionMovePlan struct {
	Moves []SectionMove `json:"moves"`
	Skips []SectionSkip `json:"skips"`
}

// sectionHeadingRE matches a level-1 or level-2 ATX heading line: where a
// section ends.
var sectionHeadingRE = regexp.MustCompile(`(?m)^#{1,2}[ \t]`)

// headingRE matches the `## <heading>` line that opens the named section,
// capturing whatever follows the heading word on that line.
func headingRE(heading string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^##[ \t]+` + regexp.QuoteMeta(heading) + `\b(.*)$`)
}

// SplitSection removes every `## <heading>` section from content and returns
// the sections' text and what remains. A section runs from its heading line to
// the next level-1 or level-2 heading, or the end. Text on the heading line
// after the heading word (`## Justification: why`) is part of the section.
// Several sections are joined with a blank line, in order, so nothing is lost.
//
// Both results are trimmed of surrounding whitespace; a non-empty remainder
// keeps one trailing newline, as a written file would. ok is false when content
// holds no such section.
func SplitSection(content, heading string) (section, rest string, ok bool) {
	re := headingRE(heading)
	var sections []string
	for {
		loc := re.FindStringSubmatchIndex(content)
		if loc == nil {
			break
		}
		start, lineEnd := loc[0], loc[1]
		inline := strings.TrimLeft(strings.TrimSpace(content[loc[2]:loc[3]]), ":")
		end := len(content)
		if next := sectionHeadingRE.FindStringIndex(content[lineEnd:]); next != nil {
			end = lineEnd + next[0]
		}
		body := strings.TrimSpace(strings.TrimSpace(inline) + "\n" + content[lineEnd:end])
		if body != "" {
			sections = append(sections, body)
		}
		before := strings.TrimRight(content[:start], " \t\n")
		after := strings.TrimLeft(content[end:], " \t\n")
		switch {
		case before == "":
			content = after
		case after == "":
			content = before
		default:
			content = before + "\n\n" + after
		}
		ok = true
	}
	if !ok {
		return "", "", false
	}
	rest = strings.TrimSpace(content)
	if rest != "" {
		rest += "\n"
	}
	return strings.Join(sections, "\n\n"), rest, true
}

// PlanSectionMoves plans moving every `## <heading>` section out of each
// project task's `from` content into its `to` content.
//
// It writes nothing. db is read for the current content — the projection is
// what the move must split — and ledger, the project's events in timestamp
// order (ReadAllEvents), for when each task's source was last written.
//
// A task already holding `to` content is skipped, not overwritten: which of the
// two texts is the real one is not something this can know.
func PlanSectionMoves(db dbQuerier, ledger []Event, project string, from, to taskcontent.Name, heading string) (SectionMovePlan, error) {
	plan := SectionMovePlan{Moves: []SectionMove{}, Skips: []SectionSkip{}}
	if from == to {
		return plan, fmt.Errorf("events: section move: source and target are both %s", from.Slug())
	}
	if strings.TrimSpace(heading) == "" {
		return plan, fmt.Errorf("events: section move: empty heading")
	}

	rows, err := db.Query(
		`SELECT c.task_id, c.content,
		        COALESCE((SELECT t2.content FROM task_content t2
		                  WHERE t2.task_id = c.task_id AND t2.name = ?), '')
		   FROM task_content c
		   JOIN tasks t ON t.id = c.task_id
		   JOIN projects p ON p.id = t.project_id
		  WHERE c.name = ? AND p.name = ?
		  ORDER BY c.task_id`,
		to.Slug(), from.Slug(), project,
	)
	if err != nil {
		return plan, fmt.Errorf("events: section move: read %s: %w", from.Slug(), err)
	}
	type candidate struct {
		id               int64
		source, existing string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.source, &c.existing); err != nil {
			rows.Close()
			return plan, fmt.Errorf("events: section move: scan: %w", err)
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return plan, fmt.Errorf("events: section move: read %s: %w", from.Slug(), err)
	}

	var lastWrite map[int64]string
	for _, c := range candidates {
		section, rest, ok := SplitSection(c.source, heading)
		if !ok {
			continue
		}
		if strings.TrimSpace(c.existing) != "" {
			plan.Skips = append(plan.Skips, SectionSkip{
				TaskID: c.id,
				Reason: fmt.Sprintf("already has %s content; merge the two by hand", to.Slug()),
			})
			continue
		}
		if section == "" {
			plan.Skips = append(plan.Skips, SectionSkip{
				TaskID: c.id,
				Reason: fmt.Sprintf("its ## %s section is empty; nothing to move", heading),
			})
			continue
		}
		if lastWrite == nil {
			if lastWrite, err = lastContentWrites(ledger, project, from); err != nil {
				return plan, err
			}
		}
		plan.Moves = append(plan.Moves, SectionMove{
			TaskID: c.id,
			TS:     lastWrite[c.id],
			Fields: map[string]string{to.Slug(): section, from.Slug(): rest},
		})
	}
	return plan, nil
}

// lastContentWrites maps each of the project's tasks to one microsecond after
// the last ledger event that wrote its `name` content, as RFC3339. It reads
// each event through the same functions both write paths apply it with, so it
// cannot disagree with them about which events wrote what.
func lastContentWrites(ledger []Event, project string, name taskcontent.Name) (map[int64]string, error) {
	last := map[int64]string{}
	for i := range ledger {
		evt := &ledger[i]
		if evt.Project != project || evt.Entity.Type != EntityTask {
			continue
		}
		writes, err := eventContentWrites(evt)
		if err != nil {
			return nil, fmt.Errorf("events: section move: read %s at %s: %w", evt.Kind, evt.TS, err)
		}
		for _, w := range writes {
			if w.Name != name {
				continue
			}
			ts, err := kairos.Parse(evt.TS)
			if err != nil {
				return nil, fmt.Errorf("events: section move: event timestamp %q: %w", evt.TS, err)
			}
			last[mustParseInt64(evt.Entity.ID)] = ts.Physical().Add(time.Microsecond).Format(time.RFC3339Nano)
		}
	}
	return last, nil
}

// eventContentWrites is the content one task event writes, by kind.
func eventContentWrites(evt *Event) ([]contentWrite, error) {
	switch evt.Kind {
	case KindTaskCreated:
		var p TaskCreatedPayload
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			return nil, err
		}
		return p.createdContent(), nil
	case KindTaskStatusChanged:
		var p TaskStatusChangedPayload
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			return nil, err
		}
		return p.statusChangedContent(), nil
	case KindTaskFieldsUpdated:
		var p TaskFieldsUpdatedPayload
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			return nil, err
		}
		newStatus := ""
		if s, ok := p.Fields["status"].(string); ok {
			newStatus = s
		}
		return contentWrites(p.Fields, newStatus)
	}
	return nil, nil
}
