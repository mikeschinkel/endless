package spawnlaunchcmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
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
	fs := refusal.NewFlags("spawn-window")
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
		auto       = fs.Bool("auto", false, "Auto-spawned (E-1814): open detached and mark the window @endless_auto_spawned")
		targetSess = fs.String("target-session", "", "tmux session id to open the window in, instead of the spawner's own")
		tmuxSess   = fs.String("tmux-session", "", "tmux session NAME to open the window in; refused if it does not exist")
		placement  = fs.String("placement", string(PlaceFirst), "Where the window's tab lands: first, last, left or right (of the session's active window)")
		noRefocus  = fs.Bool("no-refocus", false, "Open the window without making it the session's current window")
		primeDraft = fs.Bool("prime-draft", false, "Mark the window @endless_prime_draft=<task-id>: its session drafts the task's plan (E-1994)")
	)
	if err := fs.Parse(args); err != nil {
		// Text carries flag's own error line and usage block, which is what
		// stderr held before this was classified.
		refusal.NoReport(err.Error(), "Fix the flag and retry").
			Command("spawn-window").Text(fs.Output()).Exit(2)
	}

	if *windowName == "" {
		missingFlag("--window-name")
	}

	if *claudeBin == "" {
		missingFlag("--claude-bin")
	}
	if *targetSess != "" && *tmuxSess != "" {
		refusal.NoReport("spawn-window: --target-session and --tmux-session both name the session",
			"Pass one of them and retry").
			Command("spawn-window").Exit(2)
	}
	place, err := ParsePlacement(*placement)
	if err != nil {
		refusal.NoReport(fmt.Sprintf("spawn-window: --%v", err),
			"Pass --placement first, last, left or right and retry").
			Command("spawn-window").Exit(2)
	}
	if *handoff == "" {
		missingFlag("--handoff-file")
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
		AutoSpawned:    *auto,
		PrimeDraft:     *primeDraft,
	}

	specPath, err := writeSpecFile(spec)
	if err != nil {
		// The launch spec goes to a temp file, so this is an unwritable or full
		// filesystem — nothing the agent can route around by calling spawn
		// differently, and nothing was created.
		spawnWindowReport(fmt.Sprintf("spawn-window: %v", err),
			"whether to free space or repair the temp directory Endless could not write the launch spec to; no window was created").
			Exit(1)
	}

	self, err := os.Executable()
	if err != nil {
		_ = os.Remove(specPath)
		spawnWindowReport(fmt.Sprintf("spawn-window: resolve self: %v", err),
			"whether to reinstall endless-go, which could not resolve the path of its own running binary").
			Exit(1)
	}

	// Resolve the landing session BEFORE the window is asked for, so a spawn
	// that cannot say where it belongs fails without having created anything.
	//
	// --target-session names it outright by id. The auto-spawn job passes one
	// because it runs in a monitor's tmux session, and a window landing there —
	// out of sight — is the failure E-1815 designed the target setting against.
	// --tmux-session names it by the name a person sees (E-2234).
	target, err := resolveTarget(*targetSess, *tmuxSess, place)
	if err != nil {
		_ = os.Remove(specPath)
		// spawnerSession classified its own failures (see tmux_driver.go); Text
		// re-applies the `spawn-window:` prefix this site has always printed, so
		// the class travels up and the bytes do not change.
		refusal.From(err).Command("spawn-window").
			Text(fmt.Sprintf("spawn-window: %v", err)).Exit(1)
	}

	// new-window reports the window's only pane, which is claude because
	// nothing else exists in it yet. That id is what the shared layout builder
	// anchors on. Asking tmux for it afterwards BY WINDOW NAME, as this used
	// to, is the same unqualified-target bug one call later: two sessions can
	// each hold a window called `E-1705`, and tmux answers with whichever one
	// it considers current.
	cmd := []string{self, "spawn-launch", "--spec", specPath}
	claudePane, err := tmuxRunOut(newWindowArgs(target, *cwd, *windowName, *auto || *noRefocus, cmd)...)
	if err != nil {
		// The window was never created, so spawn-launch will not run to delete
		// the spec file; remove it here.
		_ = os.Remove(specPath)
		// tmux printed its own refusal just above this (runTmuxOut passes its
		// stderr through), and why a tmux server refuses a new window is not
		// something a retry answers.
		spawnWindowReport(fmt.Sprintf("spawn-window: %v", err),
			"whether tmux refusing to create the window is something they can clear; the spec file was removed and nothing was launched").
			Exit(1)
	}

	// The spawn itself has succeeded by now — claude is running in the window.
	// A missing pane id costs the layout, never the session.
	if claudePane == "" {
		refusal.NoReport("spawn-window: layout skipped (no pane id)",
			"Continue; Claude is running in the window, without the side panes").
			Command("spawn-window").Print()
		return
	}
	// Runs from THIS process, after new-window returns, because pane 0 is
	// claude: runSpawnLaunch replaces that pane via syscall.Exec and so cannot
	// orchestrate anything afterward.
	buildLayoutAround(claudePane, *cwd)
}

// resolveTarget is where new-window puts the window: the session named by id
// or by name when one is given, else the spawner's own (E-2125), and the
// position within it the placement asks for (E-2234).
func resolveTarget(sessionID, sessionName string, place Placement) (wp windowPlacement, err error) {
	var sid, active string

	switch {
	case sessionID != "":
		sid = sessionID
	case sessionName != "":
		sid, err = namedSession(sessionName)
	default:
		sid, err = spawnerSession()
	}
	if err != nil {
		goto end
	}
	if place.relative() {
		active, err = tmuxRunOut(activeWindowArgs(sid)...)
		if err == nil && active == "" {
			err = fmt.Errorf("session %s reported no active window", sid)
		}
		if err != nil {
			err = refusal.Report(
				fmt.Sprintf("resolve the active window of session %s: %v", sid, err),
				"whether tmux failing to name a live session's active window is something they can clear").
				Cause(err)
			goto end
		}
	}
	wp = placementFor(place, sid, active)

end:
	return wp, err
}

// namedSession resolves --tmux-session to a session id, refusing a name no
// session has. Checked with has-session first so a typo is told apart from a
// tmux server that will not answer.
func namedSession(name string) (sid string, err error) {
	if err = tmuxRun(hasSessionArgs(name)...); err != nil {
		err = refusal.NoReport(
			fmt.Sprintf("no tmux session is named %q", name),
			"Pass --tmux-session the exact name of an existing session (tmux ls lists them) and retry").
			Cause(err)
		goto end
	}
	sid, err = tmuxRunOut(namedSessionIDArgs(name)...)
	if err == nil && sid == "" {
		err = fmt.Errorf("session %q reported no session id", name)
	}
	if err != nil {
		err = refusal.Report(
			fmt.Sprintf("resolve tmux session %q: %v", name, err),
			"whether tmux failing to map an existing session to its id is something they can clear").
			Cause(err)
	}

end:
	return sid, err
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

// missingFlag refuses a required flag the Python caller always passes. Only a
// direct `endless-go spawn-window` invocation can omit one, so the reader is
// whoever typed it and the fix is to type it again with the flag.
func missingFlag(name string) {
	refusal.NoReport(fmt.Sprintf("spawn-window: %s is required", name),
		fmt.Sprintf("Pass %s and retry", name)).
		Command("spawn-window").Exit(1)
}

// spawnWindowReport is the shape every spawn-window fault takes: the spawn did
// not happen, the cause is outside Endless (a filesystem, a tmux server, an
// install), and the agent has no second way to ask for the same window. So each
// one names what the user has to decide rather than a retry.
func spawnWindowReport(summary, decision string) *refusal.Error {
	return refusal.Report(summary, decision).Command("spawn-window")
}
