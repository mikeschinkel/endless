package hookcmd

import (
	"fmt"
	"log"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/plancite"
)

// resumePrimedSession clears `primed` when the user resumes a primed session,
// and returns what the resumed session needs to hear about it (E-1994).
//
// It runs from UserPromptSubmit, before any tool in the resumed turn, which is
// what lets `primed` stay out of sessionstate.MayWrite: by the time the session
// can try to write, it is `working` again.
//
// The returned text is the resume's whole contract with the session:
//
//   - The task is still pre-work. A primed session binds from its worktree but
//     never claimed, so `task claim` is what starts it — and claim's gate is
//     the right gate here, because the questions this session asked at read-in
//     must be answered before the work starts.
//   - The drift check. The plan cannot have moved behind the session's back —
//     the session is what edits it, and anyone else's edit arrives as a change
//     notice — but the code can. Paths the plan cites that are gone from the
//     worktree are named; the session decides what they mean.
//
// Every failure is logged and swallowed: a resume that cannot be described is
// still a resume, and failing the hook would take the session down over a note.
func resumePrimedSession(payload claudePayload) string {
	resumed, err := monitor.ResumeFromPrimed(payload.SessionID)
	if err != nil {
		log.Printf("resuming primed session %s: %v", payload.SessionID, err)
		return ""
	}
	if !resumed {
		return ""
	}
	session, err := monitor.GetActiveSession(payload.SessionID)
	if err != nil || session == nil || session.TaskID == nil {
		return ""
	}
	taskID := *session.TaskID

	var root string
	if wt, werr := monitor.WorktreePathForTask(session.ProjectID, taskID); werr == nil && wt != "" {
		root = wt
	} else {
		root = payload.CWD
	}
	plan, perr := monitor.TaskPlan(taskID)
	if perr != nil {
		log.Printf("reading E-%d's plan for the drift check: %v", taskID, perr)
	}
	return renderPrimedResume(taskID, plan, root)
}

// renderPrimedResume composes the resume note from the task, its plan and the
// tree the plan is checked against. Split from resumePrimedSession so the text
// is testable without a database.
func renderPrimedResume(taskID int64, plan, root string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Resuming your primed session on E-%d. You read in earlier and have been waiting.\n", taskID)
	fmt.Fprintf(&b, "Start the task before working it: `endless task claim E-%d` (it refuses while a question you asked is still open).\n", taskID)

	cited := plancite.Paths(plan)
	if len(cited) == 0 {
		b.WriteString("Drift check: the plan cites no repository paths, so there is nothing to re-check mechanically.")
		return b.String()
	}
	missing := plancite.Missing(root, plan)
	if len(missing) == 0 {
		fmt.Fprintf(&b, "Drift check: all %d path(s) the plan cites still exist.", len(cited))
		return b.String()
	}
	fmt.Fprintf(&b, "Drift check: %d of the %d path(s) the plan cites no longer exist — the code moved since you read in:\n",
		len(missing), len(cited))
	for _, p := range missing {
		fmt.Fprintf(&b, "  - %s\n", p)
	}
	b.WriteString("Find where each went before relying on what you read about it, and update the plan if it changes the work.")
	return b.String()
}
