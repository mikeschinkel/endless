// Package taskcontentcmd implements the `endless-go task-content` subcommand:
// the read-only seam through which the Python CLI consumes internal/taskcontent
// (E-1531), the way taskstatuscmd serves internal/taskstatus.
//
// Touches no database — it is a pure lookup.
//
// Exit codes:
//
//	0  the verb succeeded
//	2  usage error — unknown verb or wrong arity
package taskcontentcmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/taskcontent"
)

// Run dispatches the `task-content` subcommand's inner verbs.
//
// Every refusal below is NO-REPORT, and none of them has a row in the refusal
// inventory — this verb landed after that audit, so the class comes from the
// rule itself. The one question answers all three the same way: the only ways
// to reach them are no verb, a verb this binary does not have, and a verb given
// arguments it takes none of. Each is something the caller typed, and each is
// answered by the same invocation spelled correctly. The caller is normally the
// Python CLI, which passes a fixed verb and no arguments, so a refusal here
// means a hand-run invocation or a skew between the two halves — neither needs
// the user. And nothing is read or written either (the package comment: a pure
// lookup), so there is no state for the user to decide about.
func Run(args []string) {
	if len(args) == 0 {
		// Today this prints the usage block with nothing above it, so Text pins
		// what a person reads and the summary exists only to give the agent's
		// verdict a line to carry.
		refusal.NoReport(
			"endless-go task-content: no verb given",
			"Re-run with one of the listed verbs",
		).Command("task-content").Text(usageText()).Exit(2)
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText())
	case "names":
		// One kind per line, in display order, tab-separated: slug, label.
		// Line-oriented for the reason taskstatuscmd gives — the client parses
		// no structure it would otherwise have to know about.
		if len(rest) != 0 {
			refusal.NoReport(
				fmt.Sprintf("endless-go task-content names: expected 0 argument(s), got %d", len(rest)),
				"Re-run `task-content names` with no arguments",
			).Command("task-content names").Detail(usageText()).Exit(2)
		}
		for _, n := range taskcontent.All() {
			fmt.Printf("%s\t%s\n", n.Slug(), n)
		}
	default:
		refusal.NoReport(
			fmt.Sprintf("endless-go task-content: unknown command %q", verb),
			"Re-run with one of the listed verbs",
		).Command("task-content").Detail(usageText()).Exit(2)
	}
}

// usageText is the usage block, returned rather than written: the two readers
// of it go to different places — a person asking for help reads it on stdout,
// and a refusal that carries it hands it to Text or Detail so the classified
// rendering owns the write.
func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go task-content <verb>",
		"Verbs:",
		"  names    every content kind in display order, TSV: slug label",
	}, "\n") + "\n"
}
