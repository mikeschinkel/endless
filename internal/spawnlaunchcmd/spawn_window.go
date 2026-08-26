package spawnlaunchcmd

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runSpawnWindow is the outer orchestrator — the only spawn verb Python calls.
//
// Write a JSON launch-spec file, then create a tmux window whose command is
// `<self> spawn-launch --spec <spec-path>`. The only data on the command line
// is the binary, the verb, and one shell-safe temp path. Returns once the
// window exists (tmux new-window is synchronous but does not wait for the
// window command).
//
// E-2074 removed the --attach mode, which opened a window onto a live
// background agent; background agents no longer exist.
func runSpawnWindow(args []string) {
	fs := flag.NewFlagSet("spawn-window", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		claudeBin  = fs.String("claude-bin", "", "Resolved real claude binary path")
		handoff    = fs.String("handoff-file", "", "Path to the rendered handoff file")
		permMode   = fs.String("permission-mode", "auto", "claude --permission-mode value")
		model      = fs.String("model", "", "claude --model value (optional)")
		name       = fs.String("name", "", "claude --name value (optional)")
		taskID     = fs.String("task-id", "", "Task id for @endless_task_id")
		projectID  = fs.String("project-id", "", "Project id for @endless_project_id")
		spawnedBy  = fs.String("spawned-by", "", "Spawner id for @endless_spawned_by")
		windowName = fs.String("window-name", "", "tmux window name")
		cwd        = fs.String("cwd", "", "Working directory for the window")
	)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if *windowName == "" {
		fail("spawn-window: --window-name is required")
	}

	if *claudeBin == "" {
		fail("spawn-window: --claude-bin is required")
	}
	if *handoff == "" {
		fail("spawn-window: --handoff-file is required")
	}

	spec := LaunchSpec{
		ClaudeBin:      *claudeBin,
		HandoffFile:    *handoff,
		PermissionMode: *permMode,
		Model:          *model,
		Name:           *name,
		TaskID:         *taskID,
		ProjectID:      *projectID,
		SpawnedBy:      *spawnedBy,
		WindowName:     *windowName,
		Cwd:            *cwd,
	}

	specPath, err := writeSpecFile(spec)
	if err != nil {
		fail("spawn-window: %v", err)
	}

	self, err := os.Executable()
	if err != nil {
		_ = os.Remove(specPath)
		fail("spawn-window: resolve self: %v", err)
	}

	cmd := []string{self, "spawn-launch", "--spec", specPath}
	if err = runTmux(newWindowArgs(*cwd, *windowName, cmd)...); err != nil {
		// The window was never created, so spawn-launch will not run to delete
		// the spec file; remove it here.
		_ = os.Remove(specPath)
		fail("spawn-window: %v", err)
	}

	buildSpawnLayout(*windowName, *cwd)
}

// monitorCommand is the argv run in the layout's monitor pane. `endless session
// monitor` is the verb a user would type; it is resolved to an absolute path
// when possible so the pane doesn't depend on tmux's PATH matching the
// spawner's, and left bare (letting tmux's execvp report the failure in-pane)
// when the CLI isn't on PATH at all.
func monitorCommand() []string {
	bin, err := exec.LookPath("endless")
	if err != nil {
		bin = "endless"
	}
	return []string{bin, "session", "monitor"}
}

// projectDirFor resolves the directory the monitor and shell panes start in: the
// project's MAIN checkout, even when the spawned session works in a per-task
// worktree.
//
// The Python CLI routes its DB from cwd, and those two panes are observation
// surfaces onto the main database rather than part of the branch's checkout. Run
// from inside a self_dev worktree, every ad-hoc `endless` command typed in the
// shell pane needs an explicit `--db main` to reach that database. Claude's own
// pane keeps the worktree — that one IS the branch's work.
//
// The monitor pane follows the same rule for consistency, NOT because its view
// depends on it: `session-status` pins the main DB regardless of cwd, because
// session/pane state is machine-scoped rather than project-scoped (E-698,
// c186df7d). Do not restate that as a correctness requirement — an earlier
// draft of this comment did, from a revision where the pin was briefly skipped
// inside a worktree.
//
// Falls back to cwd whenever the main checkout can't be resolved — not a git
// repo, git missing, or cwd already IS the main checkout. A cwd question must
// never stop the layout from being built.
//
// Uses the git-dir vs git-common-dir discriminator (equal in a main checkout,
// different in a linked worktree) that sandboxcmd.mainCheckoutFromWorktree and
// hookcmd.isInMainCheckout also key off.
func projectDirFor(cwd string) string {
	if cwd == "" {
		return cwd
	}
	gitDir, err := gitRevParse(cwd, "--git-dir")
	if err != nil {
		return cwd
	}
	commonDir, err := gitRevParse(cwd, "--git-common-dir")
	if err != nil {
		return cwd
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(cwd, gitDir)
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(cwd, commonDir)
	}
	if filepath.Clean(gitDir) == filepath.Clean(commonDir) {
		return cwd // already the main checkout
	}
	main := filepath.Dir(filepath.Clean(commonDir))
	if _, err = os.Stat(main); err != nil {
		return cwd
	}
	return main
}

// gitRevParse runs one `git rev-parse <arg>` in dir and returns its trimmed
// output.
func gitRevParse(dir, arg string) (string, error) {
	cmd := exec.Command("git", "rev-parse", arg)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// buildSpawnLayout turns the freshly created single-pane window into the
// canonical Endless working layout (E-1851): claude on the left at half width
// and full height, `endless session monitor` top-right, and a bare interactive
// shell bottom-right for ad-hoc endless commands. Focus ends on claude.
//
// The two right panes start in the PROJECT dir, not the window's worktree cwd —
// see projectDirFor for why the DB routing makes that the correct home for both.
//
// It runs AFTER new-window returns, from this process, because pane 0 is claude:
// runSpawnLaunch replaces that pane via syscall.Exec and so cannot orchestrate
// anything afterward.
//
// Split order matters. The SHELL pane is created first and the monitor is
// inserted ABOVE it (-b), rather than the reverse: `session monitor` shrinks its
// own pane to its frame on first paint (see sessionstatuscmd.fitPaneToFrame), so
// creating the monitor first and then splitting it would race that shrink and
// leave the shell with whatever few rows survived. Splitting the shell can't
// race anything. The monitor is therefore left at tmux's even split and sizes
// itself a moment later — which is also why nothing here has to guess a height
// it has no way to know.
//
// Best-effort throughout, matching the option-setting path: claude in pane 0 is
// the load-bearing part of a spawn, so a tmux failure here surfaces on stderr
// and returns, never fails the spawn.
func buildSpawnLayout(windowName, cwd string) {
	// The window's active pane is claude — nothing else exists in it yet.
	claudePane, err := runTmuxOut(panePaneIDArgs(windowName)...)
	if err != nil || claudePane == "" {
		fmt.Fprintf(os.Stderr, "spawn-window: layout skipped (no pane id): %v\n", err)
		return
	}

	paneDir := projectDirFor(cwd)

	shellPane, err := runTmuxOut(splitWindowArgs(claudePane, true, false, paneDir, 0, nil)...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spawn-window: layout: shell pane: %v\n", err)
		return
	}

	if _, err = runTmuxOut(splitWindowArgs(shellPane, false, true, paneDir, 0, monitorCommand())...); err != nil {
		fmt.Fprintf(os.Stderr, "spawn-window: layout: monitor pane: %v\n", err)
		// Fall through: a 2-pane window still wants focus back on claude.
	}

	if err = runTmux(selectPaneArgs(claudePane)...); err != nil {
		fmt.Fprintf(os.Stderr, "spawn-window: layout: focus claude: %v\n", err)
	}
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
