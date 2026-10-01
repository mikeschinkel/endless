// Package sessionstatecmd implements the `endless-go session-state`
// subcommand: the read-only seam through which the Python CLI consumes
// internal/sessionstate (E-2105).
//
// A near-copy of internal/taskstatuscmd, deliberately — the two vocabularies
// answer the same shapes of question, and a client that has learned one seam
// should not have to learn a second grammar for the other.
//
// One verb per package function. The alternative — a single verb emitting the
// whole registry as JSON — would force the client to know which group keys
// exist, that sql-list is a string and rank is an integer. That is structural
// knowledge of the registry living in two places, and it drifts the moment a
// group is added in Go. Here the client knows verb names and nothing else:
// adding a group needs no client change at all, because `get <group>` already
// accepts it.
//
// Touches no database — it is a pure lookup, like `task-status`.
//
// Exit codes are load-bearing for `has`, which answers by status rather than by
// output:
//
//	0  true, or the verb succeeded
//	1  false (`has` only)
//	2  usage error — unknown verb, group, state, or wrong arity
//
// Nothing written to stderr here is read where it was written.
// session_states.py._run captures it and relays it verbatim inside a
// SessionStateVocabularyError, and the registry is loaded at import time — so a
// refusal from this binary can abort every `endless` command, which is why the
// vocabulary and arity failures below are faults rather than things a reader is
// invited to retype.
package sessionstatecmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/sessionstate"
)

// Run dispatches the `session-state` subcommand's inner verbs.
func Run(args []string) {
	if len(args) == 0 {
		// The Python caller always names a verb, so a bare invocation is
		// somebody running the binary by hand — retype it and move on.
		refusal.NoReport(
			"endless-go session-state: no verb given",
			"Pass a verb and retry",
		).Command("session-state").Text(usageText()).Exit(2)
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText())
	case "groups":
		requireArgs(verb, rest, 0)
		for _, slug := range sessionstate.AllGroupSlugs() {
			fmt.Println(slug)
		}
	case "get":
		requireArgs(verb, rest, 1)
		for _, s := range sessionstate.Get(mustGroup(rest[0])) {
			fmt.Println(s)
		}
	case "has":
		requireArgs(verb, rest, 2)
		group := mustGroup(rest[0])
		// The state is validated even though a non-member would answer false
		// anyway: `has <group> <typo>` silently answering "no" is exactly the
		// omission-shaped failure this package exists to prevent.
		if !sessionstate.Has(group, mustState(rest[1])) {
			os.Exit(1)
		}
	case "sql-list":
		requireArgs(verb, rest, 1)
		fmt.Println(sessionstate.SQLList(mustGroup(rest[0])))
	case "rank":
		requireArgs(verb, rest, 2)
		fmt.Println(sessionstate.Rank(mustGroup(rest[0]), mustState(rest[1])))
	case "label":
		requireArgs(verb, rest, 1)
		fmt.Println(sessionstate.Label(mustState(rest[0])))
	case "glyph":
		// The one verb that does NOT validate its argument, and the reason is
		// the answer rather than laziness: this vocabulary defines a glyph for a
		// state outside it (sessionstate.UnknownGlyph, ⁇), so "not a state" has
		// a correct answer here where for `label` and `rank` it does not.
		// Refusing would leave the client with no way to obtain that glyph
		// except by holding a copy of it — the duplication this whole seam
		// exists to delete. A typo is not silent either: ⁇ is a
		// should-never-happen marker the eye catches immediately.
		requireArgs(verb, rest, 1)
		fmt.Println(sessionstate.Glyph(rest[0]))
	case "transitions":
		// The table as data, one edge per line, tab-separated: from, to,
		// trigger. `from` is a state, "" for a creating INSERT, or "*" for a
		// write that does not read the current state. Tab-separated rather than
		// JSON for the same reason every other verb here is line-oriented — the
		// client parses no structure it would otherwise have to know about.
		requireArgs(verb, rest, 0)
		for _, t := range sessionstate.Transitions() {
			fmt.Printf("%s\t%s\t%s\n", t.From, t.To, t.Trigger)
		}
	default:
		// Not a typo an agent can correct: session_states.py picks the verb, so
		// an unknown one means the installed endless-go predates the endless CLI
		// calling it. Only the user can reinstall the two as a matching pair,
		// and until they do, the registry fails to load and every `endless`
		// command fails with it.
		refusal.Report(
			fmt.Sprintf("endless-go session-state: unknown command %q", verb),
			"whether to reinstall endless and endless-go together as a matching pair",
		).Command("session-state").Detail(usageText()).Exit(2)
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
	refusal.Faultf("endless-go session-state %s: expected %d argument(s), got %d",
		verb, want, len(args)).
		Command("session-state " + verb).Detail(usageText()).Exit(2)
}

// mustGroup resolves a group name or exits 2 naming every valid group.
//
// The message is built in internal/sessionstate, which names no class, so From
// faults it — and a fault is the honest reading: Python asked for a group Go
// does not define, which is Python/Go drift rather than a name anybody chose.
// From rather than Fault so that classifying the constructor later takes effect
// here without a second edit.
func mustGroup(slug string) sessionstate.Group {
	g, err := sessionstate.ParseGroup(slug)
	if err != nil {
		refusal.From(err).Command("session-state").Exit(2)
	}
	return g
}

// mustState validates a state or exits 2 naming the vocabulary.
//
// Also a fault: the state reaching here came from the database or from Python
// (task_cmd.py hands `has` a value it read), so one outside the vocabulary is
// the stored data and the registry having drifted apart.
func mustState(s string) sessionstate.State {
	if err := sessionstate.Validate(s); err != nil {
		refusal.From(err).Command("session-state").Exit(2)
	}
	return s
}

func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go session-state <verb> [args...]",
		"Verbs:",
		"  groups                 list every group name, one per line",
		"  get <group>            the group's members, one per line, in group order",
		"  has <group> <state>    exit 0 if a member, 1 if not",
		"  sql-list <group>       'a','b' — for a SQL IN clause",
		"  rank <group> <state>   index within an ordered group, -1 when absent",
		"  label <state>          human display string",
		"  glyph <state>          semantic glyph (no color); " +
			sessionstate.UnknownGlyph + " for a state outside the vocabulary",
		"  transitions            the writer table, TSV: from to trigger",
		"",
		"Groups:",
		// Derived from the registry, never typed: a group added in Go shows up
		// here for free, which is the class of omission this package exists to
		// end.
		"  " + strings.Join(sessionstate.AllGroupSlugs(), ", "),
		"",
		"States:",
		"  " + strings.Join(sessionstate.Get(sessionstate.All), ", "),
	}, "\n") + "\n"
}
