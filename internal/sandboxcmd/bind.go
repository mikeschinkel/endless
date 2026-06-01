package sandboxcmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// goBinaries are wrapped with a direct exec of <worktree>/bin/<name>,
// falling back to PATH lookup if the worktree hasn't been built yet.
// Post-E-1367 this collapses to the single endless-go dispatcher; the
// subcommand argv is passed through unchanged by the wrapper.
var goBinaries = []string{
	"endless-go",
}

// retiredWrappers are files writeWrappers actively removes on each bind so
// older worktrees re-bound after a CLI self-detect lands stop carrying a
// stale wrapper that bypasses the in-process gate. E-1513 retired the
// Python wrapper (cli.DBAwareGroup re-execs under `--db sandbox` itself).
var retiredWrappers = []string{
	"endless",
}

func bindCmd(args []string) {
	fs := flag.NewFlagSet("bind", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	rest := fs.Args()
	if len(rest) < 1 || len(rest) > 2 {
		fmt.Fprintln(os.Stderr, "endless-sandbox bind: expected <worktree-path> [<sandbox-name>]")
		os.Exit(1)
	}
	worktreeRaw := rest[0]
	worktree, err := filepath.Abs(worktreeRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox bind: resolving worktree path: %v\n", err)
		os.Exit(1)
	}
	info, err := os.Stat(worktree)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox bind: worktree path %s: %v\n", worktree, err)
		os.Exit(1)
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "endless-sandbox bind: %s is not a directory\n", worktree)
		os.Exit(1)
	}

	var name string
	if len(rest) == 2 {
		name = rest[1]
	} else {
		name = defaultSandboxName(worktree)
	}
	if err := validateName(name); err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox bind: %v\n", err)
		os.Exit(1)
	}

	sandboxDir := filepath.Join(sandboxesDir(), name)
	if _, err := os.Stat(filepath.Join(sandboxDir, metaFilename)); err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox bind: sandbox %q not found at %s; run 'endless-sandbox init %s' first\n",
			name, sandboxDir, name)
		os.Exit(1)
	}

	binSandbox := filepath.Join(worktree, "bin-sandbox")
	if err := os.MkdirAll(binSandbox, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox bind: creating %s: %v\n", binSandbox, err)
		os.Exit(1)
	}

	// When the sandbox was created with `init --mode worktree`, a session row
	// was seeded. The wrapper embeds that session_id as ENDLESS_SESSION_ID so
	// bare-terminal invocations attach to the seeded row instead of hitting
	// the "Cannot determine the Endless session" guard. Empty for --mode empty
	// sandboxes (no session row); wrapper then omits the export.
	sessionID, err := readSeededSessionID(sandboxDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox bind: reading seeded session_id: %v\n", err)
		os.Exit(1)
	}

	if err := writeWrappers(binSandbox, worktree, sandboxDir, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox bind: writing wrappers: %v\n", err)
		os.Exit(1)
	}

	if err := updateClaudeSettings(worktree, binSandbox, sandboxDir); err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox bind: updating .claude/settings.json: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Bound %s → %s\n", worktree, sandboxDir)
	fmt.Printf("  wrappers: %s\n", binSandbox)
}

// defaultSandboxName derives a stable sandbox name from a worktree path.
// The sandbox dir basename equals the worktree dir basename, so each worktree
// maps 1-to-1 to its own sandbox (slug included when present).
// e.g. /path/to/.endless/worktrees/e-1281 → e-1281
//      /path/to/.endless/worktrees/e-1281-testing → e-1281-testing
func defaultSandboxName(worktree string) string {
	return filepath.Base(worktree)
}

func writeWrappers(binSandbox, worktree, sandboxDir, sessionID string) error {
	for _, name := range goBinaries {
		target := goWrapperTarget(name, worktree)
		body := goWrapperBody(sandboxDir, target, sessionID)
		if err := writeWrapper(filepath.Join(binSandbox, name), body); err != nil {
			return err
		}
	}
	for _, name := range retiredWrappers {
		path := filepath.Join(binSandbox, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing retired wrapper %s: %w", path, err)
		}
	}
	return nil
}

// goWrapperTarget returns the absolute path the wrapper should exec.
// Prefers the worktree-built binary (so candidate code is tested);
// falls back to the binary's PATH location at bind time.
func goWrapperTarget(name, worktree string) string {
	worktreeBin := filepath.Join(worktree, "bin", name)
	if info, err := os.Stat(worktreeBin); err == nil && !info.IsDir() {
		return worktreeBin
	}
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	// Best guess: still point at the worktree binary so the user gets a
	// clear "file not found" once they run it (better than silently
	// running an unexpected binary from PATH).
	return worktreeBin
}

func goWrapperBody(sandboxDir, target, sessionID string) string {
	sessionLine := ""
	if sessionID != "" {
		// Lets bare-terminal invocations attach to the row seeded by
		// `init --mode worktree`. Claude-spawned subprocesses keep minting
		// per-pane rows via the normal hook flow keyed off their own
		// CLAUDE_CODE_SESSION_ID; this export is for the terminal path only.
		sessionLine = fmt.Sprintf("export ENDLESS_SESSION_ID=%s\n", shellQuote(sessionID))
	}
	return fmt.Sprintf(`#!/usr/bin/env bash
# Generated by 'endless-sandbox bind'. Do not edit by hand.
set -euo pipefail
export XDG_CONFIG_HOME=%s
%sexec %s "$@"
`, shellQuote(sandboxDir), sessionLine, shellQuote(target))
}

// pythonWrapperBody invokes the worktree's Python source via uv. This mirrors
// the existing _endless_run shell helper (see 'endless shell-init') so
// worktree changes to src/endless/ are exercised, not the global install.
//
// XDG_CONFIG_HOME alone redirects DB writes to the sandbox; we deliberately
// do NOT export ENDLESS_SANDBOX here because that variable engages the cli.py
// guard intended for interactive 'endless sandbox enter' subshells, where
// mutating commands are refused. Worktree-bind sandboxes WANT mutations.
func pythonWrapperBody(sandboxDir, worktree string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
# Generated by 'endless-sandbox bind'. Do not edit by hand.
set -euo pipefail
export XDG_CONFIG_HOME=%s
exec uv run --directory %s endless "$@"
`, shellQuote(sandboxDir), shellQuote(worktree))
}

func writeWrapper(path, body string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o755); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// shellQuote single-quotes a value for safe shell expansion.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// updateClaudeSettings sets XDG_CONFIG_HOME in <worktree>/.claude/settings.json's
// "env" block so endless binaries invoked from a Claude session (directly or
// via hooks) route DB writes to the sandbox via inheritance. Preserves all
// other settings keys.
//
// We deliberately do NOT modify PATH here. Claude Code does not interpolate
// ${PATH} in env values, so writing "PATH": "<bin-sandbox>:${PATH}" leaves
// "${PATH}" as a literal path component and truncates the inherited PATH —
// breaking other hooks (node, etc.) and any tool invoked by Claude. The
// bin-sandbox/ wrappers are still available for explicit invocation by
// absolute path (or by user-managed `export PATH=<bin-sandbox>:$PATH`).
func updateClaudeSettings(worktree, binSandbox, sandboxDir string) error {
	_ = binSandbox // retained for signature symmetry; not used here today
	claudeDir := filepath.Join(worktree, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", claudeDir, err)
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")

	settings, err := readSettings(settingsPath)
	if err != nil {
		return err
	}

	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	env["XDG_CONFIG_HOME"] = sandboxDir
	// Remove any prior PATH manipulation a previous version of bind may have
	// written — those broke node/etc. lookups in hook subprocesses.
	delete(env, "PATH")
	settings["env"] = env

	if err := writeSettings(settingsPath, settings); err != nil {
		return err
	}
	return markSettingsSkipWorktree(worktree)
}

// markSettingsSkipWorktree sets the skip-worktree index bit on
// <worktree>/.claude/settings.json so the generated env block stays out of
// `git status` and doesn't block rebase during `endless worktree land`.
// Mirrors the contract of `just claude-settings-init`, which sets the same
// bit after it writes its hook-rewrite block.
//
// No-op if the file isn't tracked or the worktree isn't a git repository —
// downstream projects may not commit .claude/settings.json.
func markSettingsSkipWorktree(worktree string) error {
	rel := filepath.Join(".claude", "settings.json")

	chk := exec.Command("git", "-C", worktree, "ls-files", "--error-unmatch", rel)
	chk.Stdout = io.Discard
	chk.Stderr = io.Discard
	if err := chk.Run(); err != nil {
		return nil
	}

	cmd := exec.Command("git", "-C", worktree, "update-index", "--skip-worktree", rel)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git update-index --skip-worktree %s: %w: %s",
			rel, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func readSettings(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return out, nil
}

func writeSettings(path string, settings map[string]any) error {
	// Deterministic key order; settings.json is small enough that custom
	// marshaling isn't worth it for everything, but env-block keys benefit
	// from sorting so reruns produce byte-identical output.
	if env, ok := settings["env"].(map[string]any); ok {
		settings["env"] = sortedMap(env)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// sortedMap returns a map with keys in deterministic order via a JSON-marshal
// wrapper. json.Marshal sorts map[string]any keys alphabetically by default
// in Go's encoding/json, so we just need a typed alias to clarify intent.
func sortedMap(m map[string]any) map[string]any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]any, len(m))
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}
