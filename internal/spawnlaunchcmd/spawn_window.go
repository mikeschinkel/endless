package spawnlaunchcmd

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
)

// runSpawnWindow is the outer orchestrator — the only spawn verb Python calls.
//
// Normal mode: write a JSON launch-spec file, then create a tmux window whose
// command is `<self> spawn-launch --spec <spec-path>`. The only data on the
// command line is the binary, the verb, and one shell-safe temp path. Returns
// once the window exists (tmux new-window is synchronous but does not wait for
// the window command).
//
// Attach mode (--attach): create a tmux window running `<claude-bin> attach
// <short-id>` and set @endless_attached_short_id as a diagnostic (its race is
// harmless — nothing keys off it). No spec file, no handoff.
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
		attach     = fs.Bool("attach", false, "Open a window onto an existing bg agent")
		shortID    = fs.String("short-id", "", "With --attach: the bg agent short id")
	)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if *windowName == "" {
		fail("spawn-window: --window-name is required")
	}

	if *attach {
		runAttachWindow(*claudeBin, *shortID, *windowName, *cwd)
		return
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

// buildSpawnLayout turns the freshly created single-pane window into the
// canonical Endless working layout (E-1851): claude on the left at half width
// and full height, `endless session monitor` top-right, and a bare interactive
// shell bottom-right for ad-hoc endless commands. Focus ends on claude.
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

	shellPane, err := runTmuxOut(splitWindowArgs(claudePane, true, false, cwd, 0, nil)...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spawn-window: layout: shell pane: %v\n", err)
		return
	}

	if _, err = runTmuxOut(splitWindowArgs(shellPane, false, true, cwd, 0, monitorCommand())...); err != nil {
		fmt.Fprintf(os.Stderr, "spawn-window: layout: monitor pane: %v\n", err)
		// Fall through: a 2-pane window still wants focus back on claude.
	}

	if err = runTmux(selectPaneArgs(claudePane)...); err != nil {
		fmt.Fprintf(os.Stderr, "spawn-window: layout: focus claude: %v\n", err)
	}
}

// runAttachWindow opens a tmux window running `claude attach <short-id>` onto an
// already-live background agent, then records the diagnostic short-id option.
func runAttachWindow(claudeBin, shortID, windowName, cwd string) {
	if claudeBin == "" {
		fail("spawn-window --attach: --claude-bin is required")
	}
	if shortID == "" {
		fail("spawn-window --attach: --short-id is required")
	}
	attachCmd := []string{claudeBin, "attach", shortID}
	if err := runTmux(newWindowArgs(cwd, windowName, attachCmd)...); err != nil {
		fail("spawn-window --attach: %v", err)
	}
	// Diagnostic only; not load-bearing for the attach. A failure here (or a
	// race against the window's own startup) must not fail the attach.
	_ = runTmux(setOptionArgs(windowName, "@endless_attached_short_id", shortID)...)
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
