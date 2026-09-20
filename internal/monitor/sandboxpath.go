package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The per-worktree sandbox path (ED-1554).
//
// A sandbox is isolated per-worktree state that a task is exercised against: a
// throwaway database, a fixture spreadsheet, an API document, a credentials
// file that must not be the real one. Its CONTENTS are the project's business —
// endless creates an empty directory and the project's post-worktree-create
// hook fills it — so what this file owns is only the LOCATION, and there is one
// answer for every project rather than one for endless and none for anybody
// else.
//
// The sandbox lives INSIDE its worktree, which makes the path pure composition:
// a fixed relative segment from a directory the caller already has. No
// environment variable is read and none is written, so no export can outlive
// the invocation that made it and silently point a later command at another
// worktree's state — the failure that made a `~/.cache` sandbox outlive the
// worktree it belonged to and hand a session a schema weeks out of date.
//
// Python mirrors this in config.sandbox_dir / config.sandbox_config_dir. The two
// must agree byte for byte, because BOTH resolve it and the answers meet. To
// endless-go, Python threads the word `--db sandbox` and this package resolves
// it again from cwd — so a disagreement means two processes in one invocation
// reading different databases. To cmd/endless-migrate, which ED-1571 leaves no
// cwd routing, Python threads its own resolution as `--db-dir <path>` instead
// (config.migrate_db_context_args).

// sandboxDirName is the worktree-relative directory a sandbox occupies, under
// the worktree's own `.endless/`. It sits beside `tmp/` and `worktree.json`
// rather than at the worktree root so a project sees exactly one endless-owned
// directory in its tree, not two.
const sandboxDirName = "sandbox"

// projectConfig is the subset of <root>/.endless/config.json this package
// reads. Both fields are project-level and neither has a per-worktree spelling:
// a worktree that could choose its own answer to either question would be a
// worktree that could disagree with its siblings about where its state lives.
type projectConfig struct {
	// SelfDev routes endless's OWN database into the sandbox. Since ED-1554 it
	// no longer gates whether a sandbox exists — every project gets one — and
	// this is the only job it has left here.
	SelfDev bool `json:"self_dev"`

	// SandboxRoot moves this project's sandboxes out of the worktree entirely,
	// to <SandboxRoot>/<worktree-dir-name>. For the tree that cannot take extra
	// files: one whose worktree contents are themselves a git repository, a
	// build that must stay hermetic, or CI that runs `git clean -xdff` between
	// steps. Empty (the normal case) means in-tree.
	SandboxRoot string `json:"sandbox_root"`
}

// readProjectConfig reads <root>/.endless/config.json. A missing, unreadable or
// malformed file yields the zero value rather than an error: every caller here
// has a correct answer for "the project said nothing", and a project that has
// never heard of either key must behave exactly like one whose file is absent.
func readProjectConfig(root string) projectConfig {
	var cfg projectConfig
	data, err := os.ReadFile(filepath.Join(root, ".endless", "config.json"))
	if err != nil {
		return projectConfig{}
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return projectConfig{}
	}
	return cfg
}

// WorktreeRoot returns the root of the task worktree enclosing dir, or "" when
// dir is not inside one. Pure: no I/O, no existence check.
func WorktreeRoot(dir string) string {
	root := selfDevProjectRoot(dir)
	name := worktreeDirName(dir)
	if root == "" || name == "" {
		return ""
	}
	return filepath.Join(root, ".endless", "worktrees", name)
}

// WorktreeSandboxDir returns the sandbox directory of the worktree enclosing
// dir, or "" when dir is not inside a task worktree.
//
// THE resolver: every Go caller that needs a sandbox path calls this or
// WorktreeSandboxConfigDir, so the project override below is a branch inside one
// function rather than a second mechanism bolted beside the first.
//
// Reads the project config only to answer the override, so the common case is
// one small file read; callers that resolve repeatedly in a hot path should
// hold the result rather than re-resolving.
func WorktreeSandboxDir(dir string) string {
	root := selfDevProjectRoot(dir)
	name := worktreeDirName(dir)
	if root == "" || name == "" {
		return ""
	}
	if override := readProjectConfig(root).SandboxRoot; override != "" {
		return filepath.Join(expandHome(override), name)
	}
	return filepath.Join(root, ".endless", "worktrees", name, ".endless", sandboxDirName)
}

// WorktreeSandboxConfigDir returns the endless config directory inside the
// sandbox of the worktree enclosing dir, or "" when dir is not inside one.
//
// Endless appends its own "endless" segment exactly as it does to
// XDG_CONFIG_HOME, so endless's files occupy one named subdirectory of a
// sandbox it shares with whatever else the project put there. The database
// therefore lands at <worktree>/.endless/sandbox/endless/endless.db.
func WorktreeSandboxConfigDir(dir string) string {
	sandbox := WorktreeSandboxDir(dir)
	if sandbox == "" {
		return ""
	}
	return filepath.Join(sandbox, "endless")
}

// expandHome resolves a leading ~ in a configured path. Only the bare "~" and
// "~/" forms — "~user" is deliberately not supported, because a project config
// naming another user's home is far more likely to be a typo than an intent.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
