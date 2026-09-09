package sandboxcmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateClaudeSettings_WritesLocalFileOnly(t *testing.T) {
	dir := initTestGitRepo(t)
	writeSettingsFixture(t, dir, `{"enabledPlugins":{"x":false}}`)
	gitOrFatal(t, dir, "add", ".claude/settings.json")
	gitOrFatal(t, dir, "commit", "-m", "add settings")

	if err := updateClaudeSettings(dir, "/tmp/sandbox-x"); err != nil {
		t.Fatalf("updateClaudeSettings: %v", err)
	}

	local := readJSONOrFatal(t, filepath.Join(dir, localSettingsRel))
	env, ok := local["env"].(map[string]any)
	if !ok {
		t.Fatalf("expected an env block in settings.local.json, got %v", local)
	}
	if got := env["XDG_CONFIG_HOME"]; got != "/tmp/sandbox-x" {
		t.Errorf("XDG_CONFIG_HOME = %v, want /tmp/sandbox-x", got)
	}

	// The tracked file must be untouched, both on disk and in the index.
	tracked := readJSONOrFatal(t, filepath.Join(dir, trackedSettingsRel))
	if _, present := tracked["env"]; present {
		t.Errorf("bind wrote env into the tracked settings.json: %v", tracked)
	}
	assertNotSkipWorktree(t, dir)
	if out := gitOutputOrFatal(t, dir, "status", "--porcelain", "--", ".claude/settings.json"); out != "" {
		t.Errorf("tracked settings.json is modified after bind: %q", out)
	}
}

func TestUpdateClaudeSettings_PreservesExistingLocalKeys(t *testing.T) {
	dir := initTestGitRepo(t)
	writeLocalSettingsFixture(t, dir, `{"permissions":{"allow":["Bash(ls:*)"]},"env":{"PATH":"stale"}}`)

	if err := updateClaudeSettings(dir, "/tmp/sandbox-y"); err != nil {
		t.Fatalf("updateClaudeSettings: %v", err)
	}

	local := readJSONOrFatal(t, filepath.Join(dir, localSettingsRel))
	if _, ok := local["permissions"]; !ok {
		t.Errorf("hand-written permissions block was dropped: %v", local)
	}
	env := local["env"].(map[string]any)
	if _, ok := env["PATH"]; ok {
		t.Errorf("stale PATH entry should have been removed: %v", env)
	}
	if got := env["XDG_CONFIG_HOME"]; got != "/tmp/sandbox-y" {
		t.Errorf("XDG_CONFIG_HOME = %v, want /tmp/sandbox-y", got)
	}
}

func TestRepairWorktreeClaudeSettings_DisarmsRestoresAndSalvages(t *testing.T) {
	dir := armedWorktreeFixture(t)

	outcome, err := repairWorktreeClaudeSettings(dir)
	if err != nil {
		t.Fatalf("repairWorktreeClaudeSettings: %v", err)
	}
	if !outcome.Disarmed || !outcome.Restored {
		t.Fatalf("outcome = %+v, want disarmed and restored", outcome)
	}
	if strings.Join(outcome.Salvaged, ",") != "env,hooks" {
		t.Errorf("salvaged = %v, want [env hooks]", outcome.Salvaged)
	}

	assertNotSkipWorktree(t, dir)

	// The tracked file is back to exactly what HEAD holds...
	tracked := readJSONOrFatal(t, filepath.Join(dir, trackedSettingsRel))
	if _, present := tracked["env"]; present {
		t.Errorf("tracked settings.json still carries generated content: %v", tracked)
	}
	if got := gitOutputOrFatal(t, dir, "status", "--porcelain", "--", ".claude/settings.json"); got != "" {
		t.Errorf("tracked settings.json still modified after repair: %q", got)
	}

	// ...and nothing it was hiding was lost.
	local := readJSONOrFatal(t, filepath.Join(dir, localSettingsRel))
	env := local["env"].(map[string]any)
	if got := env["XDG_CONFIG_HOME"]; got != "/tmp/sandbox-legacy" {
		t.Errorf("salvaged XDG_CONFIG_HOME = %v, want /tmp/sandbox-legacy", got)
	}
	if _, ok := local["hooks"]; !ok {
		t.Errorf("salvaged settings.local.json has no hooks block: %v", local)
	}
}

// The repair must never revert an ordinary tracked-file edit: without the
// skip-worktree bit, a modified .claude/settings.json is visible user work.
func TestRepairWorktreeClaudeSettings_LeavesUnarmedModificationAlone(t *testing.T) {
	dir := initTestGitRepo(t)
	writeSettingsFixture(t, dir, `{"enabledPlugins":{"x":false}}`)
	gitOrFatal(t, dir, "add", ".claude/settings.json")
	gitOrFatal(t, dir, "commit", "-m", "add settings")
	writeSettingsFixture(t, dir, `{"enabledPlugins":{"x":true}}`)

	outcome, err := repairWorktreeClaudeSettings(dir)
	if err != nil {
		t.Fatalf("repairWorktreeClaudeSettings: %v", err)
	}
	if outcome.changed() {
		t.Fatalf("outcome = %+v, want no change on an unarmed worktree", outcome)
	}
	tracked := readJSONOrFatal(t, filepath.Join(dir, trackedSettingsRel))
	plugins := tracked["enabledPlugins"].(map[string]any)
	if plugins["x"] != true {
		t.Errorf("the user's edit was reverted: %v", tracked)
	}
	if _, err := os.Stat(filepath.Join(dir, localSettingsRel)); !os.IsNotExist(err) {
		t.Errorf("repair created settings.local.json for an unarmed worktree")
	}
}

// A key the destination already defines is the newer of the two, so the
// salvage fills gaps rather than overwriting.
func TestRepairWorktreeClaudeSettings_DoesNotOverwriteExistingLocalKeys(t *testing.T) {
	dir := armedWorktreeFixture(t)
	writeLocalSettingsFixture(t, dir, `{"env":{"XDG_CONFIG_HOME":"/tmp/sandbox-current"}}`)

	outcome, err := repairWorktreeClaudeSettings(dir)
	if err != nil {
		t.Fatalf("repairWorktreeClaudeSettings: %v", err)
	}
	if strings.Join(outcome.Salvaged, ",") != "hooks" {
		t.Errorf("salvaged = %v, want only [hooks]", outcome.Salvaged)
	}
	local := readJSONOrFatal(t, filepath.Join(dir, localSettingsRel))
	env := local["env"].(map[string]any)
	if got := env["XDG_CONFIG_HOME"]; got != "/tmp/sandbox-current" {
		t.Errorf("existing local value was overwritten: %v", got)
	}
}

func TestRepairWorktreeClaudeSettings_Idempotent(t *testing.T) {
	dir := armedWorktreeFixture(t)

	if _, err := repairWorktreeClaudeSettings(dir); err != nil {
		t.Fatalf("first repair: %v", err)
	}
	second, err := repairWorktreeClaudeSettings(dir)
	if err != nil {
		t.Fatalf("second repair: %v", err)
	}
	if second.changed() {
		t.Fatalf("second repair changed something: %+v", second)
	}
}

func TestRepairWorktreeClaudeSettings_UntrackedFileNoError(t *testing.T) {
	dir := initTestGitRepo(t)
	writeSettingsFixture(t, dir, `{}`)

	outcome, err := repairWorktreeClaudeSettings(dir)
	if err != nil {
		t.Fatalf("expected no error for untracked file, got: %v", err)
	}
	if outcome.changed() {
		t.Fatalf("outcome = %+v, want no change", outcome)
	}
}

func TestRepairWorktreeClaudeSettings_NonGitDirNoError(t *testing.T) {
	dir := t.TempDir()

	outcome, err := repairWorktreeClaudeSettings(dir)
	if err != nil {
		t.Fatalf("expected no error for non-git directory, got: %v", err)
	}
	if outcome.changed() {
		t.Fatalf("outcome = %+v, want no change", outcome)
	}
}

// --- fixtures --------------------------------------------------------------

// armedWorktreeFixture builds the pre-E-1347 arrangement: a committed
// .claude/settings.json, generated env+hooks written over it in the working
// tree, and the skip-worktree bit hiding the difference.
func armedWorktreeFixture(t *testing.T) string {
	t.Helper()
	dir := initTestGitRepo(t)
	writeSettingsFixture(t, dir, `{"enabledPlugins":{"x":false}}`)
	gitOrFatal(t, dir, "add", ".claude/settings.json")
	gitOrFatal(t, dir, "commit", "-m", "add settings")
	writeSettingsFixture(t, dir, `{
  "enabledPlugins": {"x": false},
  "env": {"XDG_CONFIG_HOME": "/tmp/sandbox-legacy"},
  "hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "/wt/bin/endless-go hook claude"}]}]}
}`)
	gitOrFatal(t, dir, "update-index", "--skip-worktree", ".claude/settings.json")
	return dir
}

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitOrFatal(t, dir, "init", "-q", "-b", "main")
	gitOrFatal(t, dir, "config", "user.email", "test@example.com")
	gitOrFatal(t, dir, "config", "user.name", "test")
	gitOrFatal(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func writeSettingsFixture(t *testing.T, dir, body string) {
	t.Helper()
	writeFixture(t, filepath.Join(dir, trackedSettingsRel), body)
}

func writeLocalSettingsFixture(t *testing.T, dir, body string) {
	t.Helper()
	writeFixture(t, filepath.Join(dir, localSettingsRel), body)
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readJSONOrFatal(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

func assertNotSkipWorktree(t *testing.T, dir string) {
	t.Helper()
	armed, err := hasSkipWorktree(dir, trackedSettingsRel)
	if err != nil {
		t.Fatalf("hasSkipWorktree: %v", err)
	}
	if armed {
		t.Fatalf("%s still carries the skip-worktree bit", trackedSettingsRel)
	}
}

func gitOrFatal(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func gitOutputOrFatal(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out)
}
