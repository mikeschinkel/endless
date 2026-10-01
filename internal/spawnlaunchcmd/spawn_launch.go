package spawnlaunchcmd

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/mikeschinkel/endless/internal/refusal"
)

// runSpawnLaunch runs inside the freshly created tmux window. It sets the
// @endless_* window options BEFORE exec — so SessionStart's option reads
// (internal/hookcmd/claude.go) are deterministic and never race the way the old
// send-keys timing did — then reads and deletes the handoff and spec files and
// execs claude with the handoff as its positional prompt.
//
// On any exec failure it exits non-zero with a clear message; it never degrades
// to send-keys (the whole point of the launcher is to have no keystroke path).
//
// # Who reads a refusal from here
//
// Nobody, as things stand. This process IS the new window's command, so
// anything it writes lands in that window — and tmux destroys a window the
// instant its command exits, with no remain-on-exit set anywhere in Endless.
// Every refusal below is therefore classified for the reader it WOULD have if
// the window ever survived: a flag nobody could have mistyped is a no-report
// usage error, an unreadable spec or handoff is Endless broken, and a claude
// binary that will not exec is the user's install.
func runSpawnLaunch(args []string) {
	fs := refusal.NewFlags("spawn-launch")
	specPath := fs.String("spec", "", "Path to the JSON launch-spec file")
	if err := fs.Parse(args); err != nil {
		refusal.NoReport(err.Error(), "Fix the flag and retry").
			Command("spawn-launch").Text(fs.Output()).Exit(2)
	}
	if *specPath == "" {
		// Unreachable in practice: spawn-window builds this argv and always
		// passes --spec.
		refusal.NoReport("spawn-launch: --spec is required", "Pass --spec and retry").
			Command("spawn-launch").Exit(1)
	}

	spec, err := readSpecFile(*specPath)
	if err != nil {
		// spawn-window wrote this file moments ago and named it on our command
		// line, so an unreadable or unparseable spec is Endless failing to talk
		// to itself — and the spawn that was already reported as succeeding
		// silently vanishes.
		refusal.Faultf("spawn-launch: %v", err).Command("spawn-launch").Exit(1)
	}

	// Publish the @endless_* window options before exec so SessionStart reads a
	// settled window. Best-effort per option: a set-option failure is logged via
	// runTmux's error but must not abort the launch (the cwd-bind fallback,
	// E-1700, still binds the session).
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		for _, optArgs := range windowOptionCommands(pane, spec) {
			_ = runTmux(optArgs...)
		}
	}

	prompt, err := os.ReadFile(spec.HandoffFile)
	if err != nil {
		// Same shape as the spec file: Endless rendered the handoff and passed
		// its path here, so failing to read it back means the handoff is never
		// delivered to a session everyone else believes started.
		refusal.Faultf("spawn-launch: read handoff %q: %v", spec.HandoffFile, err).
			Command("spawn-launch").Exit(1)
	}
	// Delete both transient files now, before exec replaces this process.
	_ = os.Remove(spec.HandoffFile)
	_ = os.Remove(*specPath)

	argv := buildClaudeArgv(spec, string(prompt))
	if err = syscall.Exec(spec.ClaudeBin, argv, os.Environ()); err != nil {
		// Almost always a claude binary that moved or was never installed at the
		// path Python resolved — the user's environment, not a call Endless can
		// make differently. The handoff and spec files are already deleted by
		// here, so there is nothing to retry with either.
		refusal.Report(fmt.Sprintf("spawn-launch: exec %q: %v", spec.ClaudeBin, err),
			"whether to reinstall or re-point the claude binary that would not exec").
			Command("spawn-launch").Exit(1)
	}
}

// buildClaudeArgv builds the argv for exec'ing claude. The permission mode is
// always present; --model and --name are added only when set; the handoff
// prompt is appended as the sole positional argument, omitted entirely when the
// handoff is empty or whitespace (a bare interactive claude).
func buildClaudeArgv(spec LaunchSpec, prompt string) []string {
	argv := []string{spec.ClaudeBin, "--permission-mode", spec.PermissionMode}
	if spec.Model != "" {
		argv = append(argv, "--model", spec.Model)
	}
	if spec.Name != "" {
		argv = append(argv, "--name", spec.Name)
	}
	if strings.TrimSpace(prompt) != "" {
		argv = append(argv, prompt)
	}
	return argv
}
