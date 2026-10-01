package sandboxcmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/refusal"
)

const minOlderThan = 24 * time.Hour

func pruneCmd(args []string) {
	fs := refusal.NewFlags("prune")
	olderThan := fs.Duration("older-than", minOlderThan, "Only prune sandboxes older than DURATION (minimum 24h)")
	parseVerbFlags(fs, "prune", args)

	if *olderThan < minOlderThan {
		refusal.NoReport(
			"endless-go sandbox prune: minimum --older-than is 24h",
			"Re-run with --older-than of at least 24h",
		).Command("sandbox prune").Exit(1)
	}

	// Unlike list, prune DELETES — so a guard it could not build is fatal.
	// Pruning unguarded would fall back to "no protections known", which is
	// exactly backwards for a destructive sweep.
	root := mainCheckoutRoot()
	guard, err := NewReapGuard(root)
	if err != nil {
		refuseUnguardedPrune(root, err)
	}

	entries, err := scanSandboxes(guard)
	if err != nil {
		refusal.Report(
			fmt.Sprintf("endless-go sandbox prune: %v", err),
			"how to clear the permission or I/O error on the sandbox cache directory",
		).Command("sandbox prune").Exit(1)
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
			refusal.NoReport(
				fmt.Sprintf("endless-go sandbox prune: sparing %s: %s", e.Meta.Name, reason),
				"Nothing to do: a protected sandbox was correctly skipped and the sweep continues",
			).Command("sandbox prune").Print()
			spared++
			continue
		}
		matched = append(matched, e)
	}

	if len(matched) == 0 {
		refusal.NoReport(
			fmt.Sprintf("endless-go sandbox prune: no orphaned sandboxes older than %s (%d spared by the reap guard)",
				*olderThan, spared),
			"Nothing to do: there was nothing to prune",
		).Command("sandbox prune").Print()
		return
	}

	names := make([]string, 0, len(matched))
	for _, e := range matched {
		names = append(names, e.Meta.Name)
	}
	refusal.Infof("Pruning %d orphaned ephemeral sandboxes older than %s: %s",
		len(matched), *olderThan, strings.Join(names, " ")).Print()

	pruned := make([]string, 0, len(matched))
	for _, e := range matched {
		if err := os.RemoveAll(e.Dir); err != nil {
			refusal.NoReport(
				fmt.Sprintf("endless-go sandbox prune: failed to remove %s: %v", e.Dir, err),
				"Nothing to do: the remaining sandboxes were pruned and a later prune retries this one",
			).Command("sandbox prune").Print()
			continue
		}
		pruned = append(pruned, e.Meta.Name)
	}
	fmt.Printf("Pruned: %s\n", strings.Join(pruned, " "))
}

// refuseUnguardedPrune refuses the sweep when the reap guard could not be
// built, and resolves in code the one thing that decides who has to hear about
// it: mainCheckoutRoot returns "" only when cwd is outside a git repository at
// all, which is a directory the agent can change and retry from. A root that
// WAS found and still failed means git or tmux would not answer in place — or
// that this repository has no `main` branch, which unmergedTaskBranches still
// hard-codes — and neither is anything a retry fixes.
func refuseUnguardedPrune(root string, err error) {
	const refusalLine = "endless-go sandbox prune: refusing to prune without a reap guard"
	summary := fmt.Sprintf("endless-go sandbox prune: %v", err)
	if root == "" {
		refusal.NoReport(summary, "cd into the project checkout and retry").
			Command("sandbox prune").Detail(refusalLine).Exit(1)
	}
	refusal.Report(summary,
		"how to repair git or tmux in place, or supply the `main` branch the guard probes for").
		Command("sandbox prune").Detail(refusalLine).Exit(1)
}
