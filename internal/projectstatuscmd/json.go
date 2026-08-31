package projectstatuscmd

import (
	"encoding/json"
	"io"
	"time"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// --json emits the board as DATA: every row the query returned, in rank order,
// each carrying its own action so a consumer can reproduce the grouping without
// re-deriving it. Uncapped, per rowcap.py's rule for machine formats — a
// consumer parsing a silently truncated payload has no footer to read and no way
// to notice.

// jsonRow is one row's wire shape. The action is the LABEL, not the glyph and
// not the enum: a glyph would force every consumer to learn the icon vocabulary,
// and the enum's numeric value is an implementation detail that reorders the
// moment a rank is inserted.
type jsonRow struct {
	Action string `json:"action"`

	TaskID      int64  `json:"task_id,omitempty"`
	Title       string `json:"title,omitempty"`
	Status      string `json:"status,omitempty"`
	Phase       string `json:"phase,omitempty"`
	Type        string `json:"type,omitempty"`
	TaskUpdated string `json:"task_updated,omitempty"`

	SessionID       int64  `json:"session_id,omitempty"`
	SessionState    string `json:"session_state,omitempty"`
	SessionActivity string `json:"session_activity,omitempty"`

	// AgeSeconds is the row's own clock (see clock()) measured against now, or
	// null when the timestamp could not be read. Included rather than left to the
	// consumer because WHICH timestamp a row ages by is a board rule — session
	// activity for a session row, task update for a task row — and a consumer
	// re-deriving it would have to reimplement that rule to agree with the view.
	AgeSeconds *int64 `json:"age_seconds"`
}

// jsonBoard is the document: the project it describes, then its rows.
type jsonBoard struct {
	Project string    `json:"project"`
	Rows    []jsonRow `json:"rows"`
}

func renderJSON(w io.Writer, project string, rows []monitor.ProjectStatusRow, now time.Time) error {
	// Uncapped, but still GROUPED and SORTED — the order is information the view
	// computed, and dropping it would make the payload strictly less useful than
	// the render for no benefit.
	groups := buildGroups(rows, 0, 0, 0, now)

	out := jsonBoard{Project: project, Rows: []jsonRow{}}
	for _, g := range groups {
		for _, r := range g.rows {
			row := jsonRow{
				Action:          g.act.label(),
				TaskID:          r.TaskID,
				Title:           r.Title,
				Status:          r.Status,
				Phase:           r.Phase,
				Type:            r.TypeSlug,
				TaskUpdated:     r.TaskUpdated,
				SessionID:       r.SessionID,
				SessionState:    r.SessionState,
				SessionActivity: r.SessionActivity,
			}
			if t := parseTS(clock(r)); !t.IsZero() {
				secs := int64(now.Sub(t).Seconds())
				if secs < 0 {
					secs = 0
				}
				row.AgeSeconds = &secs
			}
			out.Rows = append(out.Rows, row)
		}
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
