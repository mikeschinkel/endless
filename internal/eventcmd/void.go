package eventcmd

// `endless-go event void` (E-2578): the hand-written voiding.
//
// emit voids its own run when the database COMMIT fails after the ledger
// append. This command is for the run emit could not void — a process that
// died between the append and the COMMIT, a ledger written before E-2578 made
// the apply come first — and for nothing else: it is repair of the record, so
// it is the user's, and an agent is refused.
//
// It reads the named lines back before it writes anything, so an offset that
// does not start an event line is refused rather than voiding nothing while
// claiming to. It touches no database: the voiding is a fact about the ledger.

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/agentenv"
	"github.com/mikeschinkel/endless/internal/dbprovenance"
	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/refusal"
)

const voidCommand = "event void"

// offsetList collects repeated --offset flags.
type offsetList []int64

func (l *offsetList) String() string {
	parts := make([]string, len(*l))
	for i, n := range *l {
		parts[i] = strconv.FormatInt(n, 10)
	}
	return strings.Join(parts, ",")
}

func (l *offsetList) Set(v string) error {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return fmt.Errorf("--offset takes a byte offset, got %q", v)
	}
	*l = append(*l, n)
	return nil
}

// VoidAgentRefusal is the refusal an agent gets. The command is the user's.
func VoidAgentRefusal() *refusal.Error {
	return refusal.Report(
		"`endless-go event void` is the user's command: an agent does not void ledger lines. Nothing was recorded.",
		"whether the named lines never took effect, and what the voiding's reason should be",
	).Command(voidCommand)
}

func runVoid(args []string) {
	// The segment is the one positional, and flag stops at the first argument
	// that is not a flag, so it is taken before parsing when it leads and from
	// the remainder when it trails.
	var segment string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		segment, args = args[0], args[1:]
	}
	fs := refusal.NewFlags("void")
	var offsets offsetList
	fs.Var(&offsets, "offset", "Byte offset of a line to void (repeatable)")
	reason := fs.String("reason", "", "Why the lines never took effect (required)")
	project := fs.String("project", "", "Project name")
	projectRoot := fs.String("project-root", "", "Project root directory (main checkout)")
	nodeID := fs.String("node-id", "", "Kairos node ID (4-char hex)")
	actorKind := fs.String("actor-kind", "cli", "Actor kind")
	actorID := fs.String("actor-id", "", "Actor identifier")
	sessionID := fs.String("session-id", "", "Endless session ID (optional)")
	parseFlags(fs, voidCommand, args)
	if segment == "" && fs.NArg() > 0 {
		segment = fs.Arg(0)
	}

	if err := void(segment, offsets, *reason, *project, *projectRoot, *nodeID,
		*actorKind, *actorID, *sessionID, os.Stdout); err != nil {
		exitRelay(voidCommand, "endless-go event void: error: ", err)
	}
}

func void(segment string, offsets []int64, reason, project, projectRoot, nodeIDStr,
	actorKind, actorID, sessionID string, out *os.File) error {

	if agentenv.Present() {
		return VoidAgentRefusal()
	}
	if segment == "" {
		return refusal.NoReport("the segment is required",
			"Pass the segment's file name as the one positional and retry").Command(voidCommand)
	}
	for _, f := range []struct{ flag, value string }{
		{"--reason", reason}, {"--project", project}, {"--project-root", projectRoot},
		{"--node-id", nodeIDStr}, {"--actor-id", actorID},
	} {
		if f.value == "" {
			return refusal.NoReport(f.flag+" is required", "Pass "+f.flag+" and retry").Command(voidCommand)
		}
	}
	if len(offsets) == 0 {
		return refusal.NoReport("--offset is required", "Pass --offset <n> for each line to void and retry").Command(voidCommand)
	}
	nid, err := kairos.ParseNodeID(nodeIDStr)
	if err != nil {
		return refusal.Report(
			fmt.Sprintf("invalid node-id: %s", err),
			"whether to repair or remove the malformed node_id in this machine's Endless config.json (a missing one is regenerated)",
		).Command(voidCommand).Cause(err)
	}

	root := ledgerRoot(projectRoot)
	lines, err := events.ReadLinesAt(root, segment, offsets)
	if err != nil {
		return err
	}
	actor := events.EmittingActor(events.ActorKind(actorKind), actorID, sessionID)
	evt, err := events.NewVoiding(kairos.NewClock(nid), project, actor, lines, reason)
	if err != nil {
		return err
	}
	line, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal ledger.voided event: %w", err)
	}

	writer, err := events.NewWriter(root, nodeIDStr)
	if err != nil {
		return fmt.Errorf("create writer: %w", err)
	}
	if err := writer.Append(line); err != nil {
		return err
	}
	// The voiding may land in a later segment than the lines it names, which
	// is why its payload names theirs.
	if err := commitSegment(projectRoot, writer.CurrentSegment()); err != nil {
		return refusal.Report(
			fmt.Sprintf("commit ledger segment: %s", err),
			"how to clear the git failure so the voiding on disk reaches the project's history",
		).Command(voidCommand).Cause(err)
	}

	kinds := make([]string, len(lines))
	for i, l := range lines {
		kinds[i] = string(l.Event.Kind)
	}
	return dbprovenance.Encode(out, map[string]any{
		"ts":             evt.TS,
		"kind":           string(events.KindLedgerVoided),
		"entity":         evt.Entity,
		"segment":        writer.CurrentSegment(),
		"voided_segment": segment,
		"refs":           refsOf(lines),
		"voided_kinds":   kinds,
	})
}

// refsOf lists the offsets of the lines, in order.
func refsOf(lines []events.LedgerLine) []int64 {
	refs := make([]int64, len(lines))
	for i, l := range lines {
		refs[i] = l.Offset
	}
	return refs
}
