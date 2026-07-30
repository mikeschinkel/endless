package spawnlaunchcmd

import (
	"flag"
	"os"
	"strings"
	"syscall"
)

// runSpawnLaunch runs inside the freshly created tmux window. It sets the
// @endless_* window options BEFORE exec — so SessionStart's option reads
// (internal/hookcmd/claude.go) are deterministic and never race the way the old
// send-keys timing did — then reads and deletes the handoff and spec files and
// execs claude with the handoff as its positional prompt.
//
// On any exec failure it exits non-zero with a clear message; it never degrades
// to send-keys (the whole point of the launcher is to have no keystroke path).
func runSpawnLaunch(args []string) {
	fs := flag.NewFlagSet("spawn-launch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	specPath := fs.String("spec", "", "Path to the JSON launch-spec file")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *specPath == "" {
		fail("spawn-launch: --spec is required")
	}

	spec, err := readSpecFile(*specPath)
	if err != nil {
		fail("spawn-launch: %v", err)
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
		fail("spawn-launch: read handoff %q: %v", spec.HandoffFile, err)
	}
	// Delete both transient files now, before exec replaces this process.
	_ = os.Remove(spec.HandoffFile)
	_ = os.Remove(*specPath)

	argv := buildClaudeArgv(spec, string(prompt))
	if err = syscall.Exec(spec.ClaudeBin, argv, os.Environ()); err != nil {
		fail("spawn-launch: exec %q: %v", spec.ClaudeBin, err)
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
