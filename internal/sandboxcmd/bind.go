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

	if err := updateClaudeSettings(worktree, sandboxDir); err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox bind: updating .claude/settings.json: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Bound %s → %s\n", worktree, sandboxDir)
	fmt.Printf("  settings: %s\n", filepath.Join(worktree, ".claude", "settings.json"))
}

// defaultSandboxName derives a stable sandbox name from a worktree path.
// The sandbox dir basename equals the worktree dir basename, so each worktree
// maps 1-to-1 to its own sandbox (slug included when present).
// e.g. /path/to/.endless/worktrees/e-1281 → e-1281
//
//	/path/to/.endless/worktrees/e-1281-testing → e-1281-testing
func defaultSandboxName(worktree string) string {
	return filepath.Base(worktree)
}

// updateClaudeSettings sets XDG_CONFIG_HOME in <worktree>/.claude/settings.json's
// "env" block so endless binaries invoked from a Claude session (directly or
// via hooks) route DB writes to the sandbox via inheritance. Preserves all
// other settings keys.
//
// Since E-1368 the Go binary self-detects the sandbox from cwd
// (monitor.SelfDetectWorktreeSandbox), so this env block is no longer the only
// routing mechanism — but it is retained because the Python CLI still resolves
// its default config dir from XDG_CONFIG_HOME, and it keeps Go config.json/log
// reads on the sandbox even when a binary is invoked with a cwd outside the
// worktree.
//
// We deliberately do NOT modify PATH here. Claude Code does not interpolate
// ${PATH} in env values, so writing "PATH": "<dir>:${PATH}" leaves "${PATH}"
// as a literal path component and truncates the inherited PATH — breaking
// other hooks (node, etc.) and any tool invoked by Claude.
func updateClaudeSettings(worktree, sandboxDir string) error {
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
