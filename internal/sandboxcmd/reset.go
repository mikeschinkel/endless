package sandboxcmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// SeedSandboxHook is the project's sandbox-seeding hook, relative to the
// project root. Optional: a project without one gets an empty, self-ignoring
// sandbox.
//
// It is a hook of its own rather than a step of post-worktree-create.sh because
// Reset runs it on every `endless task verify`, and a create hook does things a
// verify must never repeat — Endless's own copies main's bin/endless-go over the
// worktree's, which would verify main's build instead of the candidate.
//
// Discovered in the MAIN checkout, like post-worktree-create.sh: a worktree
// on an older branch may lack the hook, or carry a stale copy of it.
const SeedSandboxHook = ".endless/hooks/seed-sandbox.sh"

// resetCmd is `endless-go sandbox reset`. Its refusals have no row in the
// refusal inventory — this verb landed after that audit — so each class comes
// from the rule: can the agent continue without asking the user?
func resetCmd(args []string) {
	fs := refusal.NewFlags("reset")
	parseVerbFlags(fs, "reset", args)
	if fs.NArg() > 0 {
		// A name was passed to the one sandbox verb that takes none, which the
		// message has always said; the retry is the same command without it.
		refusal.NoReport(
			fmt.Sprintf("endless-go sandbox reset: unexpected argument %q; "+
				"the sandbox is resolved from the current worktree and takes no name",
				fs.Arg(0)),
			"Re-run `sandbox reset` with no arguments, from inside the worktree whose sandbox you mean",
		).Command("sandbox reset").Exit(1)
	}
	cwd, err := os.Getwd()
	if err != nil {
		relayed("reset", err).Exit(1)
	}
	// Hook output goes to stderr so stdout carries only the sandbox path,
	// which keeps $(endless sandbox reset) usable. Passthrough says the hook's
	// text is the project's own: see the comment on runSeedHook.
	sandboxDir, err := Reset(cwd, refusal.Passthrough())
	if err != nil {
		// relayed is this package's one print site (dispatch.go): the class
		// travels on the error, so the cwd refusal below stays NO-REPORT and
		// the two hook refusals stay REPORT, while the clear/mkdir failures —
		// which never chose a class — come out as faults. That is the right
		// reading of them: a sandbox tree that will not be removed or recreated
		// is Endless's own tooling broken, not a command anybody mistyped.
		relayed("reset", err).Exit(1)
	}
	fmt.Println(sandboxDir)
}

// Reset returns the sandbox of the worktree enclosing dir to its seeded state:
// it clears the sandbox, adds Endless's standard contents, then runs the
// project's seed-sandbox hook. It is the single front door for resetting a
// sandbox — worktree creation and `endless task verify` both call it, and
// nothing calls the hook directly — so a gate added here later cannot be
// bypassed.
//
// Endless's standard contents are the self-ignoring .gitignore and nothing
// else. Anything specific to one project, Endless's own sandbox database
// included, is that project's hook's to add.
//
// The hook's stdout and stderr both go to out. It runs exec'd by its own
// shebang, with cwd = the worktree, argv[1] = the worktree and argv[2] = the
// sandbox, under the caller's environment. A hook that is present but not
// executable, fails to start, or exits non-zero is an error: a caller that goes
// on to run a suite against a half-seeded sandbox would report the wrong
// failure.
func Reset(dir string, out io.Writer) (string, error) {
	return ResetEnv(dir, out, nil)
}

// ResetEnv is Reset with the hook run under env instead of the caller's
// environment; a nil env inherits it, as Reset does. The verify runner passes a
// person's environment so the seeded sandbox is the same whoever starts the run
// (E-2278).
func ResetEnv(dir string, out io.Writer, env []string) (string, error) {
	worktree := monitor.WorktreeRoot(dir)
	sandboxDir := monitor.WorktreeSandboxDir(dir)
	if worktree == "" || sandboxDir == "" {
		// The caller is in the wrong directory, and that is the whole of it:
		// every caller that has a sandbox to reset can name one by running from
		// inside its worktree, so the agent moves and retries. Classified HERE,
		// on the returned value, because the print site holds only an error
		// string and refusal.From would otherwise read this as Endless failing.
		return "", refusal.NoReport(
			fmt.Sprintf("%s is not inside a task worktree (.endless/worktrees/e-NNN), so there is no sandbox to reset",
				dir),
			"Re-run from inside the task worktree whose sandbox you mean",
		)
	}
	if err := os.RemoveAll(sandboxDir); err != nil {
		return "", fmt.Errorf("clearing sandbox %s: %w", sandboxDir, err)
	}
	if err := EnsureSandboxDir(sandboxDir); err != nil {
		return "", err
	}
	// <root>/.endless/worktrees/e-NNN → <root>
	projectRoot := filepath.Dir(filepath.Dir(filepath.Dir(worktree)))
	if err := runSeedHook(projectRoot, worktree, sandboxDir, out, env); err != nil {
		return "", err
	}
	return sandboxDir, nil
}

// runSeedHook runs the project's seed-sandbox hook if it has one.
func runSeedHook(projectRoot, worktree, sandboxDir string, out io.Writer, env []string) error {
	hook := filepath.Join(projectRoot, SeedSandboxHook)
	info, err := os.Stat(hook)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("seed-sandbox hook %s: %w", hook, err)
	}
	if info.Mode()&0o111 == 0 {
		// REPORT, and the chmod is deliberately not in the summary. The hook
		// lives in the project's MAIN checkout, which a task session must not
		// edit, so "run chmod and retry" is not a retry available to the agent
		// — it is a change to the project the user owns. Text keeps the line a
		// person has always read, naming the command for them; the verdict the
		// agent reads is the summary alone, which says what is wrong and
		// nothing it could act on by itself.
		return refusal.Report(
			fmt.Sprintf("seed-sandbox hook %s is not executable", hook),
			"whether to make their project's seed-sandbox hook executable",
		).Text(fmt.Sprintf("seed-sandbox hook %s is not executable; run: chmod +x %s", hook, hook))
	}
	cmd := exec.Command(hook, worktree, sandboxDir)
	cmd.Dir = worktree
	cmd.Env = env
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		// The hook is the project's script, not Endless's code, so this is
		// neither a fault nor something the agent can retype its way past: the
		// same hook will fail the same way on every retry, and seeding is not
		// optional (a suite run against a half-seeded sandbox reports the wrong
		// failure). The hook's own output has already gone to out, above the
		// reader's eyes, which is where the diagnosis is.
		return refusal.Report(
			fmt.Sprintf("seed-sandbox hook %s failed: %v", hook, err),
			"how to fix their project's seed-sandbox hook — it ran and exited non-zero",
		).Cause(err)
	}
	return nil
}
