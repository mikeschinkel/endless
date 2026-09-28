package sandboxcmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mikeschinkel/endless/internal/monitor"
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

func resetCmd(args []string) {
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr,
			"endless-go sandbox reset: unexpected argument %q; "+
				"the sandbox is resolved from the current worktree and takes no name\n",
			fs.Arg(0))
		os.Exit(1)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-go sandbox reset: %v\n", err)
		os.Exit(1)
	}
	// Hook output goes to stderr so stdout carries only the sandbox path,
	// which keeps $(endless sandbox reset) usable.
	sandboxDir, err := Reset(cwd, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-go sandbox reset: %v\n", err)
		os.Exit(1)
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
	worktree := monitor.WorktreeRoot(dir)
	sandboxDir := monitor.WorktreeSandboxDir(dir)
	if worktree == "" || sandboxDir == "" {
		return "", fmt.Errorf(
			"%s is not inside a task worktree (.endless/worktrees/e-NNN), so there is no sandbox to reset",
			dir)
	}
	if err := os.RemoveAll(sandboxDir); err != nil {
		return "", fmt.Errorf("clearing sandbox %s: %w", sandboxDir, err)
	}
	if err := EnsureSandboxDir(sandboxDir); err != nil {
		return "", err
	}
	// <root>/.endless/worktrees/e-NNN → <root>
	projectRoot := filepath.Dir(filepath.Dir(filepath.Dir(worktree)))
	if err := runSeedHook(projectRoot, worktree, sandboxDir, out); err != nil {
		return "", err
	}
	return sandboxDir, nil
}

// runSeedHook runs the project's seed-sandbox hook if it has one.
func runSeedHook(projectRoot, worktree, sandboxDir string, out io.Writer) error {
	hook := filepath.Join(projectRoot, SeedSandboxHook)
	info, err := os.Stat(hook)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("seed-sandbox hook %s: %w", hook, err)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("seed-sandbox hook %s is not executable; run: chmod +x %s", hook, hook)
	}
	cmd := exec.Command(hook, worktree, sandboxDir)
	cmd.Dir = worktree
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("seed-sandbox hook %s failed: %w", hook, err)
	}
	return nil
}
