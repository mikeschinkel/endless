package errorscmd

// The triage verbs (E-2272): a session's answer to "we think error N is
// yours", a person's "route it now", and the lookup behind `session goto
// --error-fix`.
//
//	errors accept <id> [--session N]               this session takes it
//	errors decline <id> --reason T [--session N]   this session refuses it
//	errors escalate <id>                           route it now
//	errors fixer <id>                              (internal) who accepted it
//
// Accept and decline record WHICH session answered, so they need one: the
// session running the command (resolved exactly as a fault's raiser is), or
// --session for a person answering on a session's behalf.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/triagejob"
)

// acceptedGlyph marks an accepted incident in `errors list`: a session has
// taken it as its own to fix. Single-width, like every glyph in a table.
const acceptedGlyph = "✓"

func runAccept(args []string) {
	flags, positionals := splitArgsWith(args, "session")
	fs := refusal.NewFlags("accept")
	session := fs.Int64("session", 0, "the session taking it (default: the session running this)")
	parseFlags(fs, "errors accept", flags)

	id := oneID("accept", positionals)
	sessionID := answeringSession("accept", *session)
	if err := faults.Accept(id, sessionID); err != nil {
		answerFailure("accept", id, err)
	}
	fmt.Printf("error %d accepted by ES-%d: it is that session's to fix\n", id, sessionID)
}

func runDecline(args []string) {
	flags, positionals := splitArgsWith(args, "session", "reason")
	fs := refusal.NewFlags("decline")
	session := fs.Int64("session", 0, "the session refusing it (default: the session running this)")
	reason := fs.String("reason", "", "why it is not this session's")
	parseFlags(fs, "errors decline", flags)

	id := oneID("decline", positionals)
	if strings.TrimSpace(*reason) == "" {
		refusal.NoReport(
			"endless-go errors: decline: --reason is required",
			`Retry with --reason "<why this error is not yours>"; it goes into the bugfix task filed for it`,
		).Command("errors decline").Exit(2)
	}
	sessionID := answeringSession("decline", *session)
	if err := faults.Decline(id, sessionID, *reason); err != nil {
		answerFailure("decline", id, err)
	}
	fmt.Printf("error %d declined by ES-%d; it will be filed as a bugfix task\n", id, sessionID)
}

// runEscalate routes the incident now. A person typed it, so it skips the wait
// for the raising session to go idle, and the per-project opt-in too.
func runEscalate(args []string) {
	_, positionals := splitArgsWith(args)
	id := oneID("escalate", positionals)

	if err := faults.Escalate(id); err != nil {
		answerFailure("escalate", id, err)
	}
	outcome, err := triagejob.RouteNow(context.Background(), id)
	if triagejob.ErrSandbox(err) {
		refusal.NoReport(
			fmt.Sprintf("endless-go errors: escalate: %v", err),
			"Retry with --db main",
		).Command("errors escalate").Exit(2)
	}
	if outcome != "" {
		fmt.Println(outcome)
	}
	if err != nil {
		refusal.From(err).Command("errors escalate").
			Text(fmt.Sprintf("endless-go errors: escalate: error %d: %v", id, err)).Exit(1)
	}
	if outcome == "" {
		fmt.Printf("error %d: escalated; nothing could move yet\n", id)
	}
}

// runFixer prints the session that accepted the incident, as ES-N — the
// lookup behind `session goto --error-fix`. Exit 1, NO-REPORT, when none has.
func runFixer(args []string) {
	_, positionals := splitArgsWith(args)
	id := oneID("fixer", positionals)
	incident, ok, err := faults.Get(id)
	if err != nil {
		storeFailure("errors fixer", "endless-go errors: fixer: ", err).Exit(1)
	}
	if !ok {
		refusal.NoReport(
			fmt.Sprintf("endless-go errors: no error with id %d", id),
			"List the ids with `endless errors list` and retry with one of them",
		).Command("errors fixer").Exit(1)
	}
	if incident.AcceptedSessionID == 0 {
		refusal.NoReport(
			fmt.Sprintf("no session has accepted error %d", id),
			fmt.Sprintf("Nothing to go to yet; `endless errors show %d` says where it was routed", id),
		).Command("errors fixer").Exit(1)
	}
	fmt.Printf("ES-%d\n", incident.AcceptedSessionID)
}

func oneID(verb string, positionals []string) (id int64) {
	if len(positionals) != 1 {
		refusal.NoReport(
			fmt.Sprintf("endless-go errors: %s: name exactly one error id", verb),
			fmt.Sprintf("Retry as `endless errors %s <id>`; `endless errors list` prints the ids", verb),
		).Command("errors " + verb).Exit(2)
	}
	id, err := strconv.ParseInt(positionals[0], 10, 64)
	if err != nil || id <= 0 {
		refusal.NoReport(
			fmt.Sprintf("endless-go errors: %s: %q is not an error id", verb, positionals[0]),
			"Retry with the numeric id `endless errors list` printed",
		).Command("errors " + verb).Exit(2)
	}
	return id
}

// answeringSession is the session an answer is recorded against: the explicit
// one, else the agent session running this process. A person's shell resolves
// to none, and is told to name one rather than have an answer recorded against
// nobody.
func answeringSession(verb string, explicit int64) (sessionID int64) {
	sessionID = faults.ResolveRaiser(faults.Raiser{SessionID: explicit}).SessionID
	if sessionID == 0 {
		refusal.NoReport(
			fmt.Sprintf("endless-go errors: %s records which session answered, and no agent session is running this command; "+
				"name one with --session <N> (the number of its ES-N)", verb),
			"Run it from the Claude session that is answering, or name it with --session <N> (the number of its ES-N)",
		).Command("errors " + verb).Exit(2)
	}
	return sessionID
}

func answerFailure(verb string, id int64, err error) {
	switch {
	case errors.Is(err, faults.ErrNoIncident):
		refusal.NoReport(
			fmt.Sprintf("endless-go errors: no error with id %d", id),
			"List the ids with `endless errors list --all-projects` and retry with one of them",
		).Command("errors " + verb).Exit(1)
	case errors.Is(err, faults.ErrNotOpen):
		refusal.NoReport(
			fmt.Sprintf("endless-go errors: %s: error %d is cleared; nothing routes a cleared error", verb, id),
			"Nothing to do: a recurrence opens a new error, which is routed on its own",
		).Command("errors " + verb).Exit(1)
	}
	storeFailure("errors "+verb, fmt.Sprintf("endless-go errors: %s: ", verb), err).Exit(1)
}

// acceptedText is the listing's marker cell.
func acceptedText(incident faults.Incident) (text string) {
	if incident.AcceptedSessionID != 0 {
		text = acceptedGlyph
	}
	return text
}

// anyAccepted reports whether the marker column has anything to show.
func anyAccepted(incidents []faults.Incident) bool {
	for _, i := range incidents {
		if i.AcceptedSessionID != 0 {
			return true
		}
	}
	return false
}

// printTriage prints where the triage job routed the incident and who
// answered, under `errors show`. Nothing for an incident it never touched.
// Degraded, not refused, on a read failure: the incident is already on screen.
func printTriage(incident faults.Incident) {
	t, ok, err := faults.GetTriage(incident.ID)
	if err != nil {
		refusal.NoReport(
			"endless-go errors: triage: "+err.Error(),
			"Nothing is blocked: the incident stands, without where it was routed",
		).Command("errors show").Print()
		return
	}
	if !ok {
		return
	}
	fmt.Println()
	if t.AcceptedSessionID != 0 {
		fmt.Printf("Accepted:    %s by ES-%d — go there: endless session goto --error-fix %d\n",
			acceptedGlyph, t.AcceptedSessionID, incident.ID)
	}
	if t.DeclinedSessionID != 0 {
		fmt.Printf("Declined:    by ES-%d at %s: %s\n", t.DeclinedSessionID, t.DeclinedAt, t.DeclineReason)
	}
	if t.FixTaskID != 0 {
		fmt.Printf("Fix task:    E-%d\n", t.FixTaskID)
	}
	fmt.Printf("Triage:      %s", t.State)
	if t.Note != "" {
		fmt.Printf(" — %s", t.Note)
	}
	fmt.Println()
}
