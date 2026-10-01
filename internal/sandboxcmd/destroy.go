package sandboxcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
)

func destroyCmd(args []string) {
	fs := refusal.NewFlags("destroy")
	force := fs.Bool("force", false, "Destroy even if processes still hold files in the sandbox")
	ifExists := fs.Bool("if-exists", false, "Exit 0 silently if the sandbox does not exist (idempotent script use)")
	parseVerbFlags(fs, "destroy", args)
	rest := fs.Args()
	if len(rest) != 1 {
		refusal.NoReport(
			"endless-go sandbox destroy: expected exactly one positional arg <name>",
			"Pass exactly one sandbox name and retry",
		).Command("sandbox destroy").Exit(1)
	}
	name := rest[0]
	if err := validateName(name); err != nil {
		relayed("destroy", err).Exit(1)
	}

	dir := filepath.Join(sandboxesDir(), name)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			if *ifExists {
				return
			}
			refusal.NoReport(
				fmt.Sprintf("endless-go sandbox destroy: sandbox %q does not exist", name),
				"Check `endless-go sandbox list` for the name, or pass --if-exists when a missing sandbox is acceptable",
			).Command("sandbox destroy").Exit(1)
		}
		// Not "no such sandbox": the cache directory itself would not answer,
		// and no argv an agent can compose changes a permission or an I/O
		// error on the user's own disk.
		refusal.Report(
			fmt.Sprintf("endless-go sandbox destroy: %v", err),
			"how to clear the permission or I/O error on the sandbox cache directory",
		).Command("sandbox destroy").Exit(1)
	}

	// Defense in depth against the original E-1114 incident: even with
	// pgroup isolation, an external process (an editor with a sandbox file
	// open, a daemon descended from a different shell) could still hold
	// files. Refuse and name the offender(s) unless --force.
	if !*force {
		writers := findLiveWriters(dir)
		if len(writers) > 0 {
			refuseLiveWriters(name, writers)
		}
	}

	// Remove unconditionally — a missing or corrupt meta file is exactly
	// the case where destroy is most needed. Sanity is provided by the
	// validateName check + the path being rooted at sandboxesDir().
	if err := os.RemoveAll(dir); err != nil {
		refusal.Report(
			fmt.Sprintf("endless-go sandbox destroy: %v", err),
			"how to clear the filesystem error — permissions, a busy mount — blocking the removal",
		).Command("sandbox destroy").Exit(1)
	}
	fmt.Printf("Destroyed: %s\n", name)
}

// refuseLiveWriters refuses a destroy that would pull files out from under a
// running process.
//
// The class is REPORT unconditionally, which is a decision rather than a
// default. Whether the agent may continue turns on whose processes those PIDs
// are — its own throwaways, or the user's editor, another session, a daemon —
// and this package has no way to ask: it reads lsof, not process ancestry. The
// conservative branch is the one every other guard here takes (reap spares,
// classify falls back to in-use), and the cost of being wrong is asymmetric:
// waiting is recoverable, a killed writer and a half-written database are not.
//
// The --force escape on the last line is a bypass an agent should not be handed
// — HumanRemedy's whole purpose — but HumanRemedy renders appended to the
// summary, and this escape has always been the final line of an indented block.
// Moving it would change what a person reads, which this conversion may not do,
// so the line stays in Detail for both audiences and the REPORT verdict is what
// tells an agent not to take it. Withholding the line from an agent would be a
// change to what destroy prints rather than a conversion of it, and
// TestDestroyRefusesWithLiveWriter pins the text as it stands — so that is the
// next edit to this message, not this one.
func refuseLiveWriters(name string, writers []liveWriter) {
	lines := make([]string, 0, len(writers)+1)
	for _, w := range writers {
		lines = append(lines, fmt.Sprintf("    PID %d: %s", w.PID, w.Name))
	}
	lines = append(lines,
		"    Exit them (or 'kill <PID>') and retry, or pass --force to destroy anyway.")
	refusal.Report(
		fmt.Sprintf("endless-go sandbox destroy: refusing to destroy %q — %d process(es) still have files open in it:",
			name, len(writers)),
		"whether to stop a process it does not own, or destroy a sandbox something is still writing to",
	).Command("sandbox destroy").Detail(strings.Join(lines, "\n")).Exit(1)
}
