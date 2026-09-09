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

// The two Claude Code settings files this package touches, relative to a
// worktree root.
//
//   - trackedSettingsRel is the repository's own file. It is committed, every
//     worktree inherits it from HEAD, and endless must leave it exactly as git
//     checked it out.
//   - localSettingsRel is Claude Code's Local-tier file. It is git-ignored by
//     convention, sits ABOVE project settings in precedence, and is where every
//     generated per-worktree override belongs.
//
// Verified against Claude Code 2.1.236 before this split was made: `hooks`
// entries CONCATENATE across the two scopes (both fire) and `env` merges
// per-key with the local file winning. Both are what the override needs.
var (
	trackedSettingsRel = filepath.Join(".claude", "settings.json")
	localSettingsRel   = filepath.Join(".claude", "settings.local.json")
)

// repairOutcome records what a repair did to one worktree. The zero value means
// "nothing to do", which is the expected result for every worktree created
// after E-1347.
type repairOutcome struct {
	Worktree string
	Disarmed bool     // the skip-worktree bit was set and has been cleared
	Restored bool     // the tracked file was reverted to its committed content
	Salvaged []string // top-level keys moved into settings.local.json
}

func (o repairOutcome) changed() bool {
	return o.Disarmed || o.Restored || len(o.Salvaged) > 0
}

// claudeSettingsRepairCmd implements
// `endless-go sandbox claude-settings-repair [--all] [<worktree> ...]`.
//
// It undoes the arrangement E-1347 originally shipped: generated content written
// INTO the tracked .claude/settings.json, hidden from `git status` with
// `git update-index --skip-worktree`. That bit tells git the file must not be
// touched, so git refuses to check out any commit that CHANGES it — every live
// worktree at once, the moment main commits to that file, each reporting a
// rebase failure that names no conflicting file.
//
// It lives beside `sandbox bind` because bind is what wrote that content; this
// is the exact inverse operation on the exact same file. It is deliberately NOT
// on the user-facing `endless` CLI: only a self-dev worktree ever set the bit,
// so a downstream project would be reading a command about a state it can never
// reach.
func claudeSettingsRepairCmd(args []string) {
	fs := flag.NewFlagSet("claude-settings-repair", flag.ExitOnError)
	all := fs.Bool("all", false, "repair every worktree under the main checkout")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	targets, err := repairTargets(*all, fs.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox claude-settings-repair: %v\n", err)
		os.Exit(1)
	}

	var repaired, failed int
	for _, wt := range targets {
		outcome, err := repairWorktreeClaudeSettings(wt)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "  %s: %v\n", wt, err)
			continue
		}
		if !outcome.changed() {
			continue
		}
		repaired++
		fmt.Printf("  repaired %s%s\n", wt, salvageSuffix(outcome))
	}

	fmt.Printf("claude-settings-repair: %d of %d worktree(s) repaired", repaired, len(targets))
	if failed > 0 {
		fmt.Printf(", %d failed", failed)
	}
	fmt.Println()
	if failed > 0 {
		os.Exit(1)
	}
}

func salvageSuffix(o repairOutcome) string {
	if len(o.Salvaged) == 0 {
		return ""
	}
	return fmt.Sprintf(" (moved %s → %s)", strings.Join(o.Salvaged, ", "), localSettingsRel)
}

// repairTargets resolves the worktrees a repair run should visit: the explicit
// paths given, every worktree under the main checkout with --all, or the
// worktree containing cwd when neither is supplied.
func repairTargets(all bool, paths []string) ([]string, error) {
	if all && len(paths) > 0 {
		return nil, fmt.Errorf("--all takes no positional arguments")
	}
	if len(paths) > 0 {
		out := make([]string, 0, len(paths))
		for _, p := range paths {
			abs, err := filepath.Abs(p)
			if err != nil {
				return nil, fmt.Errorf("resolving %s: %w", p, err)
			}
			out = append(out, abs)
		}
		return out, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolving cwd: %w", err)
	}
	if !all {
		top, err := runGit(cwd, "rev-parse", "--show-toplevel")
		if err != nil {
			return nil, fmt.Errorf("cwd %s is not inside a git worktree", cwd)
		}
		return []string{top}, nil
	}

	main, err := repoMainCheckout(cwd)
	if err != nil {
		return nil, err
	}
	worktreesDir := filepath.Join(main, ".endless", "worktrees")
	entries, err := os.ReadDir(worktreesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", worktreesDir, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, filepath.Join(worktreesDir, e.Name()))
	}
	return out, nil
}

// repoMainCheckout returns the main checkout for the repository containing dir,
// whether dir is in the main checkout or in a linked worktree. Unlike
// mainCheckoutFromWorktree it does not refuse the main checkout — a --all sweep
// is normally run from there.
func repoMainCheckout(dir string) (string, error) {
	commonDir, err := runGit(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-common-dir in %s: %w", dir, err)
	}
	commonAbs, err := absPath(dir, commonDir)
	if err != nil {
		return "", err
	}
	return filepath.Dir(commonAbs), nil
}

// repairWorktreeClaudeSettings clears the legacy skip-worktree arming from one
// worktree, moving anything the arming was hiding into settings.local.json
// first so no configuration is lost.
//
// The skip-worktree bit is the ONLY trigger. Without it a modified
// .claude/settings.json is an ordinary tracked-file edit — someone's real work,
// visible in `git status` — and reverting that would be destroying user work to
// satisfy a migration. So an un-armed worktree is left untouched, which is also
// what makes repeat runs and a post-land sweep across a whole fleet safe.
//
// Order matters: salvage, then clear the bit, then restore. `git checkout --`
// will not touch a skip-worktree'd path at all, so the bit has to go first; and
// the restore discards the working content, so the salvage has to precede both.
func repairWorktreeClaudeSettings(worktree string) (repairOutcome, error) {
	out := repairOutcome{Worktree: worktree}

	// Untracked (or no repository at all) means nothing ever carried the bit.
	if !gitTracksFile(worktree, trackedSettingsRel) {
		return out, nil
	}
	armed, err := hasSkipWorktree(worktree, trackedSettingsRel)
	if err != nil {
		return out, err
	}
	if !armed {
		return out, nil
	}

	// The committed content, which the restore returns the file to. A path in
	// the index but not yet at HEAD has none; that worktree still gets the bit
	// cleared, but there is nothing to restore it to.
	head, headOK := headSettings(worktree)

	working, err := readSettings(filepath.Join(worktree, trackedSettingsRel))
	if err != nil {
		return out, err
	}
	if headOK {
		if err := salvageIntoLocal(worktree, working, head, &out); err != nil {
			return out, err
		}
	}

	if err := gitRun(worktree, "update-index", "--no-skip-worktree", trackedSettingsRel); err != nil {
		return out, err
	}
	out.Disarmed = true

	if headOK && !jsonEqual(working, head) {
		if err := gitRun(worktree, "checkout", "--", trackedSettingsRel); err != nil {
			return out, err
		}
		out.Restored = true
	}
	return out, nil
}

// salvageIntoLocal copies every top-level key whose working value differs from
// the committed one into settings.local.json, so clearing the arming does not
// silently drop the worktree's env block or hook override.
//
// A key settings.local.json already defines is left alone: the local file is
// the destination this code now writes to, so its value is the newer of the
// two. The salvage rescues what would otherwise be lost, it does not overwrite.
func salvageIntoLocal(worktree string, working, head map[string]any, out *repairOutcome) error {
	extra := make([]string, 0, len(working))
	for k, v := range working {
		if hv, ok := head[k]; ok && jsonEqualValue(v, hv) {
			continue
		}
		extra = append(extra, k)
	}
	if len(extra) == 0 {
		return nil
	}
	sort.Strings(extra)

	localPath := filepath.Join(worktree, localSettingsRel)
	local, err := readSettings(localPath)
	if err != nil {
		return err
	}
	for _, k := range extra {
		if _, exists := local[k]; exists {
			continue
		}
		local[k] = working[k]
		out.Salvaged = append(out.Salvaged, k)
	}
	if len(out.Salvaged) == 0 {
		return nil
	}
	return writeSettings(localPath, local)
}

// --- git helpers -----------------------------------------------------------

// gitTracksFile reports whether rel is in the worktree's index. False for a
// directory that is not a git repository at all, which is the case a downstream
// project reaches when it does not commit .claude/settings.json.
func gitTracksFile(worktree, rel string) bool {
	cmd := exec.Command("git", "-C", worktree, "ls-files", "--error-unmatch", rel)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

// hasSkipWorktree reports whether rel carries the skip-worktree index bit,
// which `git ls-files -v` renders as a lower-case status letter ("S" here,
// where an ordinary cached path is "H").
func hasSkipWorktree(worktree, rel string) (bool, error) {
	cmd := exec.Command("git", "-C", worktree, "ls-files", "-v", "--", rel)
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git ls-files -v %s in %s: %w", rel, worktree, err)
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "S "), nil
}

// headSettings parses the committed content of the tracked settings file. The
// bool is false when the path has no HEAD revision (a fresh repo, or a path
// added to the index but never committed).
func headSettings(worktree string) (map[string]any, bool) {
	cmd := exec.Command("git", "-C", worktree, "show", "HEAD:"+filepath.ToSlash(trackedSettingsRel))
	data, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	parsed := map[string]any{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &parsed); err != nil {
			return nil, false
		}
	}
	return parsed, true
}

func gitRun(worktree string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", worktree}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s in %s: %w: %s",
			strings.Join(args, " "), worktree, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// jsonEqual compares two settings maps by their canonical JSON encoding.
// encoding/json sorts map keys, so equal maps always marshal to equal bytes.
func jsonEqual(a, b map[string]any) bool {
	return jsonEqualValue(a, b)
}

func jsonEqualValue(a, b any) bool {
	ab, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(ab) == string(bb)
}
