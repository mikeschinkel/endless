package projectstatuscmd

import (
	"encoding/json"
	"io"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// --json emits the frame as DATA: every row, uncapped, in render order, each
// carrying the list it belongs to and its action so a consumer can reproduce
// the layout without re-deriving it. Uncapped per rowcap.py's rule for machine
// formats — a consumer parsing a silently truncated payload has no footer to
// read and no way to notice.

// jsonRow is one row's wire shape. The action is the LABEL, not the glyph and
// not the enum: a glyph would force every consumer to learn the icon
// vocabulary, and the enum's numeric value reorders the moment one is inserted.
type jsonRow struct {
	List        string `json:"list"`
	Action      string `json:"action"`
	TaskID      int64  `json:"task_id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Phase       string `json:"phase"`
	Type        string `json:"type"`
	TaskUpdated string `json:"task_updated"`
	Landed      bool   `json:"landed"`
	LiveSession bool   `json:"live_session"`
	Prompted    bool   `json:"prompted"`
}

// jsonDoc is the document: the project it describes, then its rows.
type jsonDoc struct {
	Project string    `json:"project"`
	Rows    []jsonRow `json:"rows"`
}

func renderJSON(w io.Writer, project string, rows []monitor.ProjectStatusRow, key sortKey) error {
	out := jsonDoc{Project: project, Rows: []jsonRow{}}
	for l, set := range partition(rows, key) {
		for _, r := range set {
			out.Rows = append(out.Rows, jsonRow{
				List:        listNames[l],
				Action:      classify(r).Label(),
				TaskID:      r.TaskID,
				Title:       r.Title,
				Status:      r.Status,
				Phase:       r.Phase,
				Type:        r.TypeSlug,
				TaskUpdated: r.TaskUpdated,
				Landed:      r.Landed,
				LiveSession: r.LiveSession,
				Prompted:    r.Prompted,
			})
		}
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
