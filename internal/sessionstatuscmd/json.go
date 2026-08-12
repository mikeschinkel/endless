package sessionstatuscmd

// `session status --json` (E-1914): the same row set the table draws, as data.
//
// It exists because per-session hiding needs a machine-readable surface — a
// consumer has to be able to ask "what did this session suppress?" without
// scraping glyphs out of a width-aware, color-aware, truncating renderer. Adding
// the `hidden` field to a JSON view that did not exist yet meant building the
// view; this is it.
//
// Contract, deliberately: --json emits EVERY row the query returned, each
// carrying its own hidden state, and applies no display filtering. It is data,
// not a rendering, so --show-hidden/--only-hidden — which choose what a frame
// draws — do not apply. --all DOES apply: that one filters the query (whether
// terminal-status rows are in the set at all), not the drawing.

import (
	"encoding/json"
	"io"
	"strconv"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// jsonRow is the wire shape of one task row. Field names are snake_case to match
// the rest of the CLI's JSON (`session list --json`, `task show --json`).
//
// `action` is the classifier's verdict as its machine slug ("doing", "do",
// "review", …) — the same value the table encodes as a glyph. Emitting the label
// rather than the glyph keeps consumers off the icon vocabulary, which is a
// rendering detail free to change.
type jsonRow struct {
	ID         int64  `json:"id"`
	ProjectID  int64  `json:"project_id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Phase      string `json:"phase"`
	Type       string `json:"type"`
	Action     string `json:"action"`
	HasText    bool   `json:"has_text"`
	IsFocal    bool   `json:"is_focal"`
	IsParent   bool   `json:"is_parent"`
	IsFrom     bool   `json:"is_from"`
	InFlight   bool   `json:"in_flight"`
	Landed     bool   `json:"landed"`
	Unsettled  bool   `json:"unsettled"`
	Hidden     bool   `json:"hidden"`
	HiddenAt   string `json:"hidden_at,omitempty"`
	BlockedByN int    `json:"blocked_by_n"`
	BlocksN    int    `json:"blocks_n"`
	// ReplacedBy is emitted UNGATED — the table only draws the supersession on a
	// terminal row because that is a display rule, and --json is data. A
	// consumer is entitled to the raw relation. Always present (possibly empty)
	// so an absent key never has to be read as "not replaced".
	ReplacedBy []string `json:"replaced_by"`
}

// jsonFrame wraps the rows with the ids they were resolved against, so a
// consumer can tell WHOSE view this is — which matters precisely because hidden
// is per (session, task). Without `viewer_session` a `hidden: true` would be an
// unattributed claim.
type jsonFrame struct {
	Focal         int64     `json:"focal"`
	ViewerSession int64     `json:"viewer_session"`
	Rows          []jsonRow `json:"rows"`
}

// renderJSON gathers the anchored row set, annotates it exactly as the table
// path does, and writes it to w as one JSON object. Rows are sorted with the
// same comparator the table uses so the two surfaces agree on order.
func renderJSON(w io.Writer, a anchor, all bool) error {
	rows, err := gatherRows(a.focal, a.parentSession, a.emittingSession, all)
	if err != nil {
		return err
	}
	monitor.AnnotateSessionStatusUnsettled(rows)
	if err := annotateHidden(rows, a.emittingSession); err != nil {
		return err
	}
	sortRows(rows)

	// Non-nil so an empty result marshals as [] rather than null — a consumer
	// iterating the rows should not have to special-case "no work".
	out := jsonFrame{
		Focal:         a.focal,
		ViewerSession: a.emittingSession,
		Rows:          make([]jsonRow, 0, len(rows)),
	}
	for _, r := range rows {
		replaced := make([]string, 0, len(r.ReplacedBy))
		for _, id := range r.ReplacedBy {
			replaced = append(replaced, "E-"+strconv.FormatInt(id, 10))
		}
		out.Rows = append(out.Rows, jsonRow{
			ID:         r.ID,
			ProjectID:  r.ProjectID,
			Title:      collapse(r.Title),
			Status:     r.Status,
			Phase:      r.Phase,
			Type:       r.TypeSlug,
			Action:     classify(r).label(),
			HasText:    r.HasText,
			IsFocal:    r.IsFocal,
			IsParent:   r.IsParent,
			IsFrom:     r.IsFrom,
			InFlight:   r.InFlight,
			Landed:     r.Landed,
			Unsettled:  r.Unsettled,
			Hidden:     r.Hidden,
			HiddenAt:   r.HiddenAt,
			BlockedByN: r.BlockedByN,
			BlocksN:    r.BlocksN,
			ReplacedBy: replaced,
		})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
