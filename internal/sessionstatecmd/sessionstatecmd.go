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
package sessionstatecmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mikeschinkel/endless/internal/sessionstate"
)

// Run dispatches the `session-state` subcommand's inner verbs.
func Run(args []string) {
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "-h", "--help", "help":
		usage(os.Stdout)
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
		fmt.Fprintf(os.Stderr, "endless-go session-state: unknown command %q\n", verb)
		usage(os.Stderr)
		os.Exit(2)
	}
}

// requireArgs exits 2 with a usage message when a verb's arity is wrong.
func requireArgs(verb string, args []string, want int) {
	if len(args) == want {
		return
	}
	fmt.Fprintf(os.Stderr, "endless-go session-state %s: expected %d argument(s), got %d\n",
		verb, want, len(args))
	usage(os.Stderr)
	os.Exit(2)
}

// mustGroup resolves a group name or exits 2 naming every valid group.
func mustGroup(slug string) sessionstate.Group {
	g, err := sessionstate.ParseGroup(slug)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	return g
}

// mustState validates a state or exits 2 naming the vocabulary.
func mustState(s string) sessionstate.State {
	if err := sessionstate.Validate(s); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	return s
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: endless-go session-state <verb> [args...]")
	fmt.Fprintln(w, "Verbs:")
	fmt.Fprintln(w, "  groups                 list every group name, one per line")
	fmt.Fprintln(w, "  get <group>            the group's members, one per line, in group order")
	fmt.Fprintln(w, "  has <group> <state>    exit 0 if a member, 1 if not")
	fmt.Fprintln(w, "  sql-list <group>       'a','b' — for a SQL IN clause")
	fmt.Fprintln(w, "  rank <group> <state>   index within an ordered group, -1 when absent")
	fmt.Fprintln(w, "  label <state>          human display string")
	fmt.Fprintln(w, "  glyph <state>          semantic glyph (no color); "+
		sessionstate.UnknownGlyph+" for a state outside the vocabulary")
	fmt.Fprintln(w, "  transitions            the writer table, TSV: from to trigger")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Groups:")
	// Derived from the registry, never typed: a group added in Go shows up here
	// for free, which is the class of omission this package exists to end.
	fmt.Fprintln(w, "  "+strings.Join(sessionstate.AllGroupSlugs(), ", "))
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "States:")
	fmt.Fprintln(w, "  "+strings.Join(sessionstate.Get(sessionstate.All), ", "))
}
