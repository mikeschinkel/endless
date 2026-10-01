// Package taskstatuscmd implements the `endless-go task-status` subcommand: the
// read-only seam through which the Python CLI consumes internal/taskstatus
// (E-1891).
//
// One verb per package function, deliberately. The alternative — a single verb
// emitting the whole registry as JSON — would force the client to know which
// group keys exist, that sql-list is a string and rank is an integer. That is
// structural knowledge of the registry living in two places, and it drifts the
// moment a group is added in Go. Here the client knows verb names and nothing
// else: adding a group needs no client change at all, because `get <group>`
// already accepts it.
//
// Touches no database — it is a pure lookup, like `markdown render`.
//
// Exit codes are load-bearing for `has`, which answers by status rather than by
// output:
//
//	0  true, or the verb succeeded
//	1  false (`has` only)
//	2  usage error — unknown verb, group, status, or wrong arity
//
// Nothing written to stderr here is read where it was written. statuses.py._run
// captures it and relays it verbatim inside a StatusVocabularyError, and the
// registry is loaded at import time — so a refusal from this binary can abort
// every `endless` command, which is why the vocabulary and arity failures below
// are faults rather than things a reader is invited to retype.
package taskstatuscmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// Run dispatches the `task-status` subcommand's inner verbs.
func Run(args []string) {
	if len(args) == 0 {
		// The Python caller always names a verb, so a bare invocation is
		// somebody running the binary by hand — retype it and move on.
		refusal.NoReport(
			"endless-go task-status: no verb given",
			"Pass a verb and retry",
		).Command("task-status").Text(usageText()).Exit(2)
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText())
	case "groups":
		requireArgs(verb, rest, 0)
		for _, slug := range taskstatus.AllGroupSlugs() {
			fmt.Println(slug)
		}
	case "get":
		requireArgs(verb, rest, 1)
		for _, s := range taskstatus.Get(mustGroup(rest[0])) {
			fmt.Println(s)
		}
	case "has":
		requireArgs(verb, rest, 2)
		group := mustGroup(rest[0])
		// The status is validated even though a non-member would answer false
		// anyway: `has <group> <typo>` silently answering "no" is exactly the
		// omission-shaped failure this package exists to prevent.
		if !taskstatus.Has(group, mustStatus(rest[1])) {
			os.Exit(1)
		}
	case "sql-list":
		requireArgs(verb, rest, 1)
		fmt.Println(taskstatus.SQLList(mustGroup(rest[0])))
	case "rank":
		requireArgs(verb, rest, 2)
		fmt.Println(taskstatus.Rank(mustGroup(rest[0]), mustStatus(rest[1])))
	case "label":
		requireArgs(verb, rest, 1)
		fmt.Println(taskstatus.Label(mustStatus(rest[0])))
	case "glyph":
		requireArgs(verb, rest, 1)
		fmt.Println(taskstatus.Glyph(mustStatus(rest[0])))
	case "transitions":
		// The table as data, one edge per line, tab-separated:
		// from, to, actor, types (slash-joined, empty = every type), label.
		// Tab-separated rather than JSON for the same reason every other verb
		// here is line-oriented — the client parses no structure it would
		// otherwise have to know about.
		requireArgs(verb, rest, 0)
		for _, t := range taskstatus.Transitions() {
			slugs := make([]string, len(t.Types))
			for i, tt := range t.Types {
				slugs[i] = tt.String()
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\n",
				t.From, t.To, t.Actor, strings.Join(slugs, "/"), t.Label)
		}
	case "lifecycle":
		// The generated body of docs/status-lifecycle.mmd. `just
		// lifecycle-index` writes it between the file's generated markers;
		// `just lifecycle-check` fails when the committed artifact has drifted.
		requireArgs(verb, rest, 0)
		fmt.Print(taskstatus.RenderMermaid())
	default:
		// Not a typo an agent can correct: statuses.py picks the verb, so an
		// unknown one means the installed endless-go predates the endless CLI
		// calling it. Only the user can reinstall the two as a matching pair,
		// and until they do, the registry fails to load and every `endless`
		// command fails with it.
		refusal.Report(
			fmt.Sprintf("endless-go task-status: unknown command %q", verb),
			"whether to reinstall endless and endless-go together as a matching pair",
		).Command("task-status").Detail(usageText()).Exit(2)
	}
}

// requireArgs exits 2 with a usage message when a verb's arity is wrong.
//
// A fault rather than a usage refusal, because of who can reach it: the Python
// wrapper builds this argv itself, so wrong arity is the two halves of Endless
// disagreeing about a verb's shape, not something anybody mistyped.
func requireArgs(verb string, args []string, want int) {
	if len(args) == want {
		return
	}
	refusal.Faultf("endless-go task-status %s: expected %d argument(s), got %d",
		verb, want, len(args)).
		Command("task-status " + verb).Detail(usageText()).Exit(2)
}

// mustGroup resolves a group name or exits 2 naming every valid group.
//
// The message is built in internal/taskstatus, which names no class, so From
// faults it — and a fault is the honest reading: Python asked for a group Go
// does not define, which is Python/Go drift rather than a name anybody chose.
// From rather than Fault so that classifying the constructor later takes effect
// here without a second edit.
func mustGroup(slug string) taskstatus.Group {
	g, err := taskstatus.ParseGroup(slug)
	if err != nil {
		refusal.From(err).Command("task-status").Exit(2)
	}
	return g
}

// mustStatus validates a status or exits 2 naming the vocabulary.
//
// Also a fault: the status reaching here came from the database or from Python
// (task_cmd.py hands `has` a status it read), so one outside the vocabulary is
// the stored data and the registry having drifted apart.
func mustStatus(s string) taskstatus.Status {
	if err := taskstatus.Validate(s); err != nil {
		refusal.From(err).Command("task-status").Exit(2)
	}
	return s
}

func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go task-status <verb> [args...]",
		"Verbs:",
		"  groups                  list every group name, one per line",
		"  get <group>             the group's members, one per line, in group order",
		"  has <group> <status>    exit 0 if a member, 1 if not",
		"  sql-list <group>        'a','b' — for a SQL IN / NOT IN clause",
		"  rank <group> <status>   index within an ordered group, -1 when absent",
		"  label <status>          human display string",
		"  glyph <status>          semantic glyph (no color)",
		"  transitions             the lifecycle edge table, TSV: from to actor types label",
		"  lifecycle               the generated mermaid body of docs/status-lifecycle.mmd",
		"",
		"Groups:",
		// Derived from the registry, never typed: a group added in Go shows up
		// here for free, which is the class of omission E-1891 exists to end.
		"  " + strings.Join(taskstatus.AllGroupSlugs(), ", "),
		"",
		"Statuses:",
		"  " + strings.Join(taskstatus.Get(taskstatus.All), ", "),
	}, "\n") + "\n"
}
