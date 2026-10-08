package triagejob

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mikeschinkel/endless/internal/faults"
)

// triageMessage is what a suspected session is told. It says "we think", not
// "you did": sessions are good at telling whether a bug is theirs, and the
// incident is handled only once one says so.
func triageMessage(in faults.Incident) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Endless error triage: we think error %d is yours.\n\n", in.ID)
	fmt.Fprintf(&b, "  %s (%s): %s\n", in.Code, in.Title(), in.Summary)
	fmt.Fprintf(&b, "  raised by %s, %d occurrence(s), last seen %s UTC\n\n",
		raiserText(in.TaskID, in.SessionID), in.Occurrences, in.LastSeenAt)
	fmt.Fprintf(&b, "Investigate it: endless errors show %d --detail\n\n", in.ID)
	fmt.Fprintf(&b, "If it is yours, run `endless errors accept %d` and fix it within your own task "+
		"(set your task to revisit if it already shipped). If it is not, run "+
		"`endless errors decline %d --reason \"<why>\"`, and a bugfix session will take it. "+
		"Answer either way: an error nobody accepts is filed as a bugfix task.", in.ID, in.ID)
	return b.String()
}

// senderPrompt is the throwaway `claude -p` sender's whole job: find the agent
// in the given tmux pane and SendMessage it the text, then say what happened in
// one word the job can parse. Finding by pane rather than by name covers every
// naming scheme a session has ever had, and Endless already knows the pane.
func senderPrompt(pane, message string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a one-shot message relay for Endless. Do exactly this and nothing else:\n")
	fmt.Fprintf(&b, "1. Call ListAgents (load it and SendMessage with ToolSearch first if they are deferred).\n")
	fmt.Fprintf(&b, "2. Find the one agent whose tmux location ends with the pane id %s "+
		"(it is listed like `tmux <session>:@<window>.%s`).\n", pane, pane)
	fmt.Fprintf(&b, "3. Send that agent the message between the markers below, verbatim, with SendMessage.\n\n")
	fmt.Fprintf(&b, "Then end your reply with exactly one of these words on a line of its own:\n")
	fmt.Fprintf(&b, "%s - SendMessage accepted the message for delivery now\n", delivered)
	fmt.Fprintf(&b, "%s - SendMessage said the message is held, queued, or will be delivered later\n", held)
	fmt.Fprintf(&b, "%s - no listed agent is in pane %s\n", notFound, pane)
	fmt.Fprintf(&b, "%s - anything else\n\n", failed)
	fmt.Fprintf(&b, "<<<MESSAGE\n%s\nMESSAGE>>>\n", message)
	return b.String()
}

// parseDelivery reads the sender's verdict from its last non-empty line.
// Anything unrecognised is a failure: a sender that cannot say it delivered
// did not deliver, as far as routing is concerned.
func parseDelivery(out string) delivery {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.ToUpper(strings.Trim(strings.TrimSpace(lines[i]), "*`.:!- "))
		if line == "" {
			continue
		}
		for _, d := range []delivery{delivered, held, notFound, failed} {
			if line == string(d) || strings.HasPrefix(line, string(d)+" ") {
				return d
			}
		}
		return failed
	}
	return failed
}

// raiserText names a raiser the way `errors list` does.
func raiserText(taskID, sessionID int64) string {
	switch {
	case sessionID != 0 && taskID != 0:
		return fmt.Sprintf("ES-%d (E-%d)", sessionID, taskID)
	case sessionID != 0:
		return fmt.Sprintf("ES-%d", sessionID)
	case taskID != 0:
		return fmt.Sprintf("E-%d", taskID)
	}
	return "nobody Endless could identify"
}

// taskSpec is a bugfix task to file.
type taskSpec struct {
	title       string
	description string
	context     string
	plan        string
	cleansUp    []int64
}

// titleLimit keeps a filed task's title to one listing row.
const titleLimit = 90

func fixTitle(in faults.Incident) string {
	title := fmt.Sprintf("Fix %s: %s", in.Code, strings.TrimSpace(in.Summary))
	if r := []rune(title); len(r) > titleLimit {
		title = string(r[:titleLimit-1]) + "…"
	}
	return title
}

func fixDescription(in faults.Incident) string {
	return fmt.Sprintf("Find and fix the cause of error %d (%s, from %s), which the fault-triage job "+
		"filed because no session that raised it took it on.", in.ID, in.Code, in.Source)
}

// fixPlan is the same for every filed fix: spawning needs a plan, and the
// diagnosis is the session's to make. The job does no diagnosis of its own.
func fixPlan() string {
	return strings.Join([]string{
		"1. Accept every error listed in the context with `endless errors accept <id>`, so the error list shows it is being fixed.",
		"2. Diagnose from the context and `endless errors show <id> --detail`.",
		"3. Decide whether the condition is a bug, or routine and should never have been an error at all (ED-1614). In the second case the fix is to the fault's producer, not to the condition.",
		"4. Fix the cause, add a test that would have caught it, and hand it off for verification.",
		"5. If the diagnosis needs a decision only the user can make, stop and ask rather than guess.",
	}, "\n")
}

// detailsFunc reads one incident's logged occurrences.
type detailsFunc func(errorID int64) []faults.Detail

// detailLimit is how many of an incident's most recent occurrences a fix
// task's context carries in full. The rest are a command away.
const detailLimit = 3

// fixContext is the evidence a fix session starts from, gathered without a
// model: each incident's record, who raised it, why it was routed here, and its
// most recent captures.
func fixContext(incidents []faults.Routable, details detailsFunc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Filed by the fault-triage job (E-2272). Every error below is this task's to fix; "+
		"a recurrence of one of them is added here rather than filed again.\n")

	sort.SliceStable(incidents, func(i, j int) bool { return incidents[i].Incident.ID < incidents[j].Incident.ID })
	for _, r := range incidents {
		in, t := r.Incident, r.Triage
		fmt.Fprintf(&b, "\n## Error %d\n\n", in.ID)
		fmt.Fprintf(&b, "- Code: %s (%s), %s\n", in.Code, in.Title(), in.Severity)
		fmt.Fprintf(&b, "- Source: %s; fingerprint %s\n", in.Source, in.Fingerprint)
		fmt.Fprintf(&b, "- Occurrences: %d, first seen %s, last seen %s (UTC)\n", in.Occurrences, in.FirstSeenAt, in.LastSeenAt)
		fmt.Fprintf(&b, "- Summary: %s\n", in.Summary)
		fmt.Fprintf(&b, "- Raised by: %s\n", raiserText(in.TaskID, in.SessionID))
		if in.ClearedAt != "" {
			fmt.Fprintf(&b, "- Cleared: %s\n", in.ClearedAt)
		}
		switch {
		case t.State == faults.TriageRecorded:
			fmt.Fprintf(&b, "- How it got here: raised by this fix task's own work\n")
		case t.DeclinedSessionID != 0:
			fmt.Fprintf(&b, "- How it got here: ES-%d declined it: %s\n", t.DeclinedSessionID, orNone(t.DeclineReason))
		case t.Note != "":
			fmt.Fprintf(&b, "- How it got here: %s\n", t.Note)
		}

		var ds []faults.Detail
		if details != nil {
			ds = details(in.ID)
		}
		if len(ds) > detailLimit {
			ds = ds[len(ds)-detailLimit:]
		}
		for _, d := range ds {
			fmt.Fprintf(&b, "\n### Occurrence %d, %s\n", d.Occurrence, d.TS)
			if d.Detail != "" {
				fmt.Fprintf(&b, "\n```\n%s\n```\n", strings.TrimSpace(d.Detail))
			}
			keys := make([]string, 0, len(d.Fields))
			for k := range d.Fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&b, "- %s: %v\n", k, d.Fields[k])
			}
		}
	}

	fmt.Fprintf(&b, "\n## Before fixing\n\n")
	fmt.Fprintf(&b, "Run `endless errors accept <id>` for each error above first: it records that this "+
		"session took it. If the diagnosis is that the condition is routine and should never have been "+
		"an error, the fix is to the fault's producer, not to the condition (ED-1614).\n")
	return b.String()
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(no reason given)"
	}
	return s
}
