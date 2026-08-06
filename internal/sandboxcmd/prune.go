package sandboxcmd

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

const minOlderThan = 24 * time.Hour

func pruneCmd(args []string) {
	fs := flag.NewFlagSet("prune", flag.ExitOnError)
	olderThan := fs.Duration("older-than", minOlderThan, "Only prune sandboxes older than DURATION (minimum 24h)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if *olderThan < minOlderThan {
		fmt.Fprintln(os.Stderr, "endless-sandbox prune: minimum --older-than is 24h")
		os.Exit(1)
	}

	// Unlike list, prune DELETES — so a guard it could not build is fatal.
	// Pruning unguarded would fall back to "no protections known", which is
	// exactly backwards for a destructive sweep.
	guard, err := NewReapGuard(mainCheckoutRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox prune: %v\n", err)
		fmt.Fprintln(os.Stderr, "endless-sandbox prune: refusing to prune without a reap guard")
		os.Exit(1)
	}

	entries, err := scanSandboxes(guard)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox prune: %v\n", err)
		os.Exit(1)
	}

	var matched []listEntry
	var spared int
	for _, e := range entries {
		if e.State != stateOrphaned {
			continue
		}
		if e.Age < *olderThan {
			continue
		}
		// Re-check the guard at the point of deletion rather than trusting the
		// state classify() computed. The two agree today, but a destructive
		// sweep must not depend on that staying true.
		protected, reason := guard.Protected(e.Meta.Name)
		if protected {
			fmt.Fprintf(os.Stderr, "endless-sandbox prune: sparing %s: %s\n", e.Meta.Name, reason)
			spared++
			continue
		}
		matched = append(matched, e)
	}

	if len(matched) == 0 {
		fmt.Fprintf(os.Stderr, "endless-sandbox prune: no orphaned sandboxes older than %s (%d spared by the reap guard)\n",
			*olderThan, spared)
		return
	}

	names := make([]string, 0, len(matched))
	for _, e := range matched {
		names = append(names, e.Meta.Name)
	}
	fmt.Fprintf(os.Stderr, "Pruning %d orphaned ephemeral sandboxes older than %s: %s\n",
		len(matched), *olderThan, strings.Join(names, " "))

	pruned := make([]string, 0, len(matched))
	for _, e := range matched {
		if err := os.RemoveAll(e.Dir); err != nil {
			fmt.Fprintf(os.Stderr, "endless-sandbox prune: failed to remove %s: %v\n", e.Dir, err)
			continue
		}
		pruned = append(pruned, e.Meta.Name)
	}
	fmt.Printf("Pruned: %s\n", strings.Join(pruned, " "))
}
