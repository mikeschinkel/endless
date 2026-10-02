package events_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/taskcontent"
)

func TestSplitSection(t *testing.T) {
	for _, tc := range []struct {
		name, content, section, rest string
		ok                           bool
	}{
		{"absent", "just prose\n", "", "", false},
		{"only the section", "## Justification\n\nwhy\n", "why", "", true},
		{
			"section first, prose after",
			"## Justification\n\nwhy\n\n## From the description\n\nmore\n",
			"why", "## From the description\n\nmore\n", true,
		},
		{
			"prose first, section last",
			"intro\n\n## Justification\n\nwhy\nsecond line\n",
			"why\nsecond line", "intro\n", true,
		},
		{
			"section between two others",
			"a\n\n## Justification\n\nwhy\n\n# Top\n\nb\n",
			"why", "a\n\n# Top\n\nb\n", true,
		},
		{"text on the heading line", "## Justification: why\n", "why", "", true},
		{"a level-3 heading stays inside", "## Justification\n\nwhy\n\n### detail\n\nx\n", "why\n\n### detail\n\nx", "", true},
		{"two sections are joined", "## Justification\n\none\n\n## Other\n\nmid\n\n## Justification\n\ntwo\n", "one\n\ntwo", "## Other\n\nmid\n", true},
		{"a longer word is not the heading", "## Justifications\n\nno\n", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			section, rest, ok := events.SplitSection(tc.content, "Justification")
			if ok != tc.ok || section != tc.section || rest != tc.rest {
				t.Errorf("SplitSection(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.content, section, rest, ok, tc.section, tc.rest, tc.ok)
			}
		})
	}
}

// TestSectionMove_LiveAndRebuildAgree is the property the move is built on: it
// is emitted as ordinary events, so rebuilding the projection from the ledger
// must reproduce exactly the content the live apply produced.
//
// Task 801 is the case that decides the event's timestamp. Its notes were
// rewritten after the justification was first filed — 18 of the 29 real tasks
// look like this. A move stamped at the FIRST write would replay before that
// rewrite on rebuild, and the rewrite would put the heading back.
func TestSectionMove_LiveAndRebuildAgree(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	var ledger []events.Event
	at := func(minutes int) string { return kairos.New(base.Add(time.Duration(minutes)*time.Minute), 0, 0x1234).String() }
	created := func(id int64, minutes int, notes string) {
		p := events.TaskCreatedPayload{Title: "t", Phase: "now", Status: "unplanned", Type: "research", Notes: notes}
		ledger = append(ledger, taskEvent(t, events.KindTaskCreated, id, at(minutes), p))
	}
	fields := func(id int64, minutes int, f map[string]any) {
		ledger = append(ledger, taskEvent(t, events.KindTaskFieldsUpdated, id, at(minutes),
			events.TaskFieldsUpdatedPayload{Fields: f}))
	}

	created(800, 0, "## Justification\n\nonly this\n")
	created(801, 1, "## Justification\n\nfirst\n")
	fields(801, 5, map[string]any{"notes": "## Justification\n\nfirst\n\n## From the description\n\nkept prose\n"})
	fields(801, 9, map[string]any{"title": "renamed later"})
	created(802, 2, "no section here\n")

	liveDB := withExecutorDB(t)
	for i := range ledger {
		if _, err := events.Execute(&ledger[i], nil); err != nil {
			t.Fatalf("Execute %s on E-%s: %v", ledger[i].Kind, ledger[i].Entity.ID, err)
		}
	}

	plan, err := events.PlanSectionMoves(liveDB, ledger, "test",
		taskcontent.Notes, taskcontent.Justification, "Justification")
	if err != nil {
		t.Fatalf("PlanSectionMoves: %v", err)
	}
	if len(plan.Moves) != 2 || len(plan.Skips) != 0 {
		t.Fatalf("plan = %+v, want 2 moves and no skips", plan)
	}
	byID := map[int64]events.SectionMove{}
	for _, m := range plan.Moves {
		byID[m.TaskID] = m
	}
	if want := base.Add(5*time.Minute + time.Microsecond).Format(time.RFC3339Nano); byID[801].TS != want {
		t.Errorf("E-801 move stamped %s, want %s — one microsecond after the LAST notes write", byID[801].TS, want)
	}

	// Emit the moves as `event emit --ts` would stamp them, and apply each live.
	for _, m := range plan.Moves {
		ts, perr := time.Parse(time.RFC3339, m.TS)
		if perr != nil {
			t.Fatalf("move TS %q is not RFC3339: %v", m.TS, perr)
		}
		f := map[string]any{}
		for k, v := range m.Fields {
			f[k] = v
		}
		evt := taskEvent(t, events.KindTaskFieldsUpdated, m.TaskID, kairos.New(ts, 0, 0x5678).String(),
			events.TaskFieldsUpdatedPayload{Fields: f})
		if _, err = events.Execute(&evt, nil); err != nil {
			t.Fatalf("Execute move on E-%d: %v", m.TaskID, err)
		}
		ledger = append(ledger, evt)
	}

	dir := t.TempDir()
	w, err := events.NewWriter(dir, "c0de")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for _, evt := range ledger {
		appendEvent(t, w, evt)
	}
	tempPath, result, err := events.ProjectToTempDB(dir)
	if err != nil {
		t.Fatalf("ProjectToTempDB: %v", err)
	}
	t.Cleanup(func() { os.Remove(tempPath) })
	if len(result.Errors) > 0 {
		t.Fatalf("projection reported errors: %v", result.Errors)
	}
	rebuiltDB, err := sql.Open("sqlite", tempPath)
	if err != nil {
		t.Fatalf("open projected db: %v", err)
	}
	defer rebuiltDB.Close()

	live, rebuilt := contentRows(t, liveDB), contentRows(t, rebuiltDB)
	if live != rebuilt {
		t.Fatalf("live and rebuilt content differ.\nlive:\n%s\nrebuilt:\n%s", live, rebuilt)
	}
	want := strings.Join([]string{
		"800 justification=only this",
		"801 justification=first",
		"801 notes=## From the description\n\nkept prose\n",
		"802 notes=no section here\n",
	}, "\n")
	if live != want {
		t.Errorf("content after the move:\n%s\nwant:\n%s", live, want)
	}

	// Repeatable: a second plan finds nothing left to move.
	again, err := events.PlanSectionMoves(liveDB, ledger, "test",
		taskcontent.Notes, taskcontent.Justification, "Justification")
	if err != nil {
		t.Fatalf("second PlanSectionMoves: %v", err)
	}
	if len(again.Moves) != 0 || len(again.Skips) != 0 {
		t.Errorf("second plan = %+v, want nothing", again)
	}
}

// A task that already holds target content is reported, never overwritten.
func TestSectionMove_SkipsAnExistingTarget(t *testing.T) {
	db := withExecutorDB(t)
	evt := taskEvent(t, events.KindTaskCreated, 810, kairos.New(time.Now(), 0, 1).String(),
		events.TaskCreatedPayload{
			Title: "t", Phase: "now", Status: "unplanned", Type: "research",
			Justification: "the row", Notes: "## Justification\n\nthe heading\n",
		})
	if _, err := events.Execute(&evt, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	plan, err := events.PlanSectionMoves(db, []events.Event{evt}, "test",
		taskcontent.Notes, taskcontent.Justification, "Justification")
	if err != nil {
		t.Fatalf("PlanSectionMoves: %v", err)
	}
	if len(plan.Moves) != 0 || len(plan.Skips) != 1 || plan.Skips[0].TaskID != 810 {
		raw, _ := json.Marshal(plan)
		t.Errorf("plan = %s, want E-810 skipped", raw)
	}
}
