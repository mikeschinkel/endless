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
package taskstatuscmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// Run dispatches the `task-status` subcommand's inner verbs.
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
	default:
		fmt.Fprintf(os.Stderr, "endless-go task-status: unknown command %q\n", verb)
		usage(os.Stderr)
		os.Exit(2)
	}
}

// requireArgs exits 2 with a usage message when a verb's arity is wrong.
func requireArgs(verb string, args []string, want int) {
	if len(args) == want {
		return
	}
	fmt.Fprintf(os.Stderr, "endless-go task-status %s: expected %d argument(s), got %d\n",
		verb, want, len(args))
	usage(os.Stderr)
	os.Exit(2)
}

// mustGroup resolves a group name or exits 2 naming every valid group.
func mustGroup(slug string) taskstatus.Group {
	g, err := taskstatus.ParseGroup(slug)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	return g
}

// mustStatus validates a status or exits 2 naming the vocabulary.
func mustStatus(s string) taskstatus.Status {
	if err := taskstatus.Validate(s); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	return s
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: endless-go task-status <verb> [args...]")
	fmt.Fprintln(w, "Verbs:")
	fmt.Fprintln(w, "  groups                  list every group name, one per line")
	fmt.Fprintln(w, "  get <group>             the group's members, one per line, in group order")
	fmt.Fprintln(w, "  has <group> <status>    exit 0 if a member, 1 if not")
	fmt.Fprintln(w, "  sql-list <group>        'a','b' — for a SQL IN / NOT IN clause")
	fmt.Fprintln(w, "  rank <group> <status>   index within an ordered group, -1 when absent")
	fmt.Fprintln(w, "  label <status>          human display string")
	fmt.Fprintln(w, "  glyph <status>          semantic glyph (no color)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Groups:")
	// Derived from the registry, never typed: a group added in Go shows up here
	// for free, which is the class of omission E-1891 exists to end.
	fmt.Fprintln(w, "  "+strings.Join(taskstatus.AllGroupSlugs(), ", "))
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Statuses:")
	fmt.Fprintln(w, "  "+strings.Join(taskstatus.Get(taskstatus.All), ", "))
}
