package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/dbcontext"
	"github.com/mikeschinkel/endless/internal/gatekind"
	"github.com/mikeschinkel/endless/internal/processkind"
	"github.com/mikeschinkel/endless/internal/schema"
	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

var (
	dbOnce = &sync.Once{}
	dbConn *sql.DB
	dbErr  error

	// dbPathOverride, when set, forces DBPath() to a fixed location regardless
	// of XDG_CONFIG_HOME routing. Set once by ForceRealDB() at process entry.
	dbPathOverride string

	// dbContextDir, when set, pins ConfigDir() (and therefore DBPath()) to an
	// explicit directory for the lifetime of this process. It is the E-1429
	// "explicit DB context": the Python CLI resolves the user's --db
	// main|worktree choice to a directory and threads it to every Go
	// subprocess via the --config-dir flag (ConsumeDBContextFlag). Inside a
	// self-dev worktree, guardWorktreeDBContext() refuses to open the DB
	// unless an explicit context exists (this var or dbPathOverride).
	//
	// Deliberately NOT satisfied by XDG_CONFIG_HOME: an env var can be
	// exported once and silently route every later command to the wrong DB --
	// the exact failure mode the gate exists to kill. Only a per-invocation
	// flag (or the hook's ForceRealDB) counts as explicit.
	dbContextDir string

	// dbContextFromFlag distinguishes an EXPLICIT --config-dir (the trustworthy
	// per-invocation flag, or a test deliberately targeting a DB) from a
	// cwd-self-detected sandbox (SelfDetectWorktreeSandbox). Both set
	// dbContextDir so ConfigDir()/the E-1429 gate follow the same target, but
	// only the explicit flag should suppress the hook/tmux PinMainDB
	// override. Without this split a self-dev worktree's own dev session would
	// have its session/pane-state writes routed to the sandbox (where the
	// spawned task does not exist -> task_id FK-fails -> NULL -> status
	// line shows "claim a task"), instead of the main database per E-1450 (E-1700).
	dbContextFromFlag bool
)

// ConfigDir returns the Endless configuration directory. When an explicit DB
// context was provided (--config-dir, via ConsumeDBContextFlag), it wins over
// XDG_CONFIG_HOME so config.json and logs follow the same target as the DB.
//
// The resolution itself lives in internal/dbcontext, because ED-1571's
// migration-only executable needs the same answer and may not link this
// package to get it — importing internal/monitor would put the whole
// application, schema-applying connect included, into a binary whose safety
// rests on carrying nothing but migrations. This is the routing layer on top:
// the explicit context, and (in DBPath) the hook/tmux main pin.
func ConfigDir() string {
	return string(dbcontext.ConfigDir(dt.DirPath(dbContextDir)))
}

// CacheDir returns the Endless cache directory.
func CacheDir() string {
	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheDir, "endless")
}

// IsSandboxActive reports whether the current process is reading/writing
// through an E-1281 per-worktree sandbox. Detection: ConfigDir() resolves
// under CacheDir()/sandboxes/. ForceRealDB() uses this to decide whether
// hook-fired DB writes must be redirected to the real database. (Originally
// added for the plan-snapshot sandbox-skip removed in E-1449; reintroduced
// here as E-1362's ledger entry anticipated it might be "useful elsewhere".)
// See E-1450.
func IsSandboxActive() bool {
	sandboxRoot := filepath.Join(CacheDir(), "sandboxes")
	rel, err := filepath.Rel(sandboxRoot, ConfigDir())
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// DBPath returns the path to the Endless SQLite database.
func DBPath() string {
	if dbPathOverride != "" {
		return dbPathOverride
	}
	return string(dbcontext.DBPath(dt.DirPath(dbContextDir)))
}

// ForceRealDB routes monitor.DB() and DBPath()-derived artifacts (e.g. backups)
// to the real database under ~/.config/endless, ignoring the E-1281 sandbox
// XDG_CONFIG_HOME routing. It overrides only the DB path: log files and global
// config.json reads keep following ConfigDir(), and because XDG_CONFIG_HOME is
// never mutated, IsSandboxActive() still reports true for any other
// sandbox-aware behavior. The endless-hook binary calls this at startup so
// hook-fired writes (session registration, activity, state transitions) reflect
// real-world activity and land in the real DB rather than throwaway sandbox
// fixtures. No-op when not sandbox-routed; must be called before the first
// DB()/DBPath() use. See E-1450.
func ForceRealDB() {
	if !IsSandboxActive() {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dbPathOverride = filepath.Join(home, ".config", "endless", "endless.db")
}

// HasExplicitDBContext reports whether an EXPLICIT --config-dir was consumed for
// this process (as opposed to a cwd-self-detected sandbox). Callers that would
// otherwise PinMainDB use this to let an explicit per-invocation DB target win —
// the E-1429 contract is that an explicit flag is trustworthy and beats the
// env-driven main pin. Production invokers of the pinned binaries (tmux, the
// Claude hook, tmux) never pass --config-dir, so this stays false
// there and the main pin still applies — including for a self-dev worktree's own
// dev session, whose sandbox is discovered from cwd (SelfDetectWorktreeSandbox),
// not from a flag, so it must NOT suppress the pin (E-1700). Only tests / sandbox
// tooling that pass --config-dir flip it true.
func HasExplicitDBContext() bool {
	return dbContextFromFlag
}

// SetDBContextDir records an EXPLICIT DB/config directory for this process,
// satisfying the E-1429 self-dev-worktree gate and marking the context as
// flag-provided so it beats the hook/tmux main pin. Called by
// ConsumeDBContextFlag when the Python CLI threads --config-dir to a Go
// subprocess (and by tests that deliberately target a DB). Self-detection from
// cwd uses setDetectedContextDir instead, which does NOT set the flag.
func SetDBContextDir(dir string) {
	dbContextDir = dir
	dbContextFromFlag = true
}

// setDetectedContextDir records a cwd-self-detected sandbox as the config/DB
// context WITHOUT marking it flag-explicit. It satisfies ConfigDir() and the
// E-1429 gate the same way SetDBContextDir does, but leaves HasExplicitDBContext
// false so the hook/tmux PinMainDB override still moves the DB to main
// (E-1450/E-1700). Only SelfDetectWorktreeSandbox calls this.
func setDetectedContextDir(dir string) {
	dbContextDir = dir
}

// PinMainDB unconditionally routes the DB (DBPath() and DB()) to the real
// database under ~/.config/endless and satisfies the E-1429 worktree gate.
//
// It differs from ForceRealDB in two ways that matter for binaries invoked
// outside a Claude session's env injection:
//   - Unconditional: ForceRealDB only redirects when IsSandboxActive() (i.e.
//     XDG_CONFIG_HOME points into a sandbox). `endless-go tmux` is invoked by
//     tmux itself, where XDG may be unset; the conditional check would miss
//     and the gate would refuse it.
//   - DB-path only: ConfigDir() is left untouched, so config.json and logs
//     keep following XDG_CONFIG_HOME (the worktree's sandbox). Only the DB
//     itself moves to main, matching the E-1450 split — session/pane state is
//     real-world activity and belongs in the main database.
//
// Used by the always-main infrastructure surfaces (`endless-go tmux`). Must
// precede the first DB()/DBPath() use. The hook keeps
// ForceRealDB(): its XDG is always the sandbox, so the conditional path
// already lands on main.
func PinMainDB() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dbPathOverride = filepath.Join(home, ".config", "endless", "endless.db")
}

// ConsumeDBContextFlag strips a "--config-dir <dir>" / "--config-dir=<dir>"
// pair out of os.Args (wherever it appears) and records it as the explicit DB
// context. Binaries call this once at the top of main() so their existing
// positional argument parsing (os.Args[1] = subcommand) is unaffected.
//
// The DB target is carried as a per-invocation flag, never an env var: an
// exported env var could silently satisfy the gate for every later command,
// which is exactly the silent-wrong-DB failure mode E-1429 exists to prevent.
func ConsumeDBContextFlag() {
	cleaned, dir, found := dbcontext.ConsumeConfigDirFlag(os.Args)
	if found {
		SetDBContextDir(string(dir))
	}
	os.Args = cleaned
}

// dbContextExplicit reports whether this process was handed an explicit DB
// target: the --config-dir flag (dbContextDir) or the hook's ForceRealDB
// override (dbPathOverride). Either satisfies the self-dev-worktree gate.
func dbContextExplicit() bool {
	return dbContextDir != "" || dbPathOverride != ""
}

// pinnedToForeignRealDB reports whether this process has been pinned onto the
// real database at ~/.config/endless via ForceRealDB() (the Claude hook) or
// PinMainDB() (`endless-go tmux`) — the automatic entry points that
// redirect a sandbox/worktree-context binary's DATA writes onto the main database
// (E-1450/E-1700). The pin is signalled by dbPathOverride != "".
//
// Invariant (E-1818): only a database's OWNING binary applies schema.SQL (DDL +
// seed) and the enum integrity gates to it. A candidate (self-dev worktree)
// binary pinned here for session-state writes opens the real DB schema-passive:
// it uses the deployed schema as-is and never mutates structure or seed rows,
// and never runs the fail-close enum integrity checks against a schema it does
// not own. Otherwise an unlanded binary could migrate — or, via a destructive
// schema.SQL, corrupt — a real DB it does not own the instant a hook fires,
// before any land, review, or explicit action (the E-1659 incident).
//
// The gate is DB-path only: it does not fire for the deployed global binary or
// a self-detected sandbox open of a DB the binary owns (dbPathOverride == ""),
// nor for an explicit --config-dir open (which sets dbContextDir, not
// dbPathOverride) — so land-time `endless db apply-change` still migrates.
func pinnedToForeignRealDB() bool {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	}
	return foreignRealDB(dbPathOverride, resolvedPath(exe), DBPath(), realDBPath())
}

// foreignRealDB is the decision itself, pure so it can be proven without a
// process, a home directory, or a database.
//
// override != "" is E-1818's original case: ForceRealDB / PinMainDB moved this
// process onto the main database, so it does not own the schema.
//
// The second clause is E-1975's. An explicit --config-dir is trusted to ROUTE
// this process (E-1429: a per-invocation flag beats the env), but routing and
// OWNERSHIP are different questions, and conflating them punched a hole through
// E-1818's invariant. `endless --db main <anything>` threads
// --config-dir <main database> to every endless-go shellout; run from a worktree
// that is the WORKTREE's binary — unlanded code — and because the explicit flag
// left override empty this returned false, so monitor.DB() applied the branch's
// schema.SQL to the user's real database. A branch that adds a table created it
// in the main database the first time an agent ran a routine command, days before
// the branch landed and whether or not it ever did.
//
// Ownership is decided by what the executable IS, not by how it was pointed: a
// binary built inside a task worktree may write DATA to the main database
// (session and pane state is real-world activity, per E-1450) and may never
// migrate, reseed or fail-close it. The deployed binary is not a candidate, so
// it still creates the schema after the branch lands — the table appears one
// land later, which is exactly when it should.
//
// realPath == "" means the home directory could not be resolved. That falls
// back to the override answer rather than guessing, because a wrong guess in
// the permissive direction is the bug this exists to stop and a wrong guess in
// the strict direction would leave a fresh install with no schema.
func foreignRealDB(override, exePath, dbPath, realPath string) bool {
	if override != "" {
		return true
	}
	if realPath == "" || dbPath != realPath {
		return false
	}
	return strings.Contains(exePath, worktreePathMarker)
}

// candidateBuild reports whether this executable was built inside a task
// worktree, i.e. whether it is unlanded code.
//
// Keyed on the EXECUTABLE's path rather than cwd. cwd answers "where is the
// user working", which is a different question and the wrong one: the global
// binary invoked from inside a worktree is still the deployed build and owns
// the schema, while the worktree's binary invoked from anywhere does not.
func candidateBuild() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(resolvedPath(exe), worktreePathMarker)
}

// realDBPath is the deployed installation's database, independent of any routing
// in force. It hardcodes the same location PinMainDB does, so "the main database"
// means one thing across both.
func realDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "endless", "endless.db")
}

// PinnedToRealDB reports whether this process has been pinned onto a fixed real
// database (PinMainDB / ForceRealDB), overriding any sandbox routing.
//
// Exported for the E-698 job runner, which must not execute jobs when a
// self_dev worktree's candidate build is pointed at the developer's main
// database. Combined with InSelfDevWorktree it names exactly that state; on its
// own it is true for ordinary pinned surfaces (hook, tmux) in the main
// checkout too, where running jobs is correct.
func PinnedToRealDB() bool { return pinnedToForeignRealDB() }

// worktreePathMarker is the path segment that identifies an endless-managed
// task worktree: <project-root>/.endless/worktrees/e-NNN.
const worktreePathMarker = "/.endless/worktrees/"

// selfDevProjectRoot returns the project root (the main checkout) when dir is
// inside one of its .endless/worktrees/e-* worktrees, or "" otherwise.
func selfDevProjectRoot(dir string) string {
	i := strings.Index(dir, worktreePathMarker)
	if i < 0 {
		return ""
	}
	// Confirm the segment names a task worktree (e-NNN), not some unrelated
	// directory that happens to contain the marker substring.
	if TaskIDFromWorktreePath(dir) == "" {
		return ""
	}
	return dir[:i]
}

// worktreeDirName returns the worktree directory basename (e-NNN) when dir
// is inside a task worktree, or "" otherwise. The per-worktree sandbox dir
// basename equals the worktree dir basename, so this is the lowercase
// `e-NNN` form used to locate the sandbox — distinct from
// TaskIDFromWorktreePath's canonical `E-NNN` task id. Mirrors Python
// config.worktree_dir_name. Pure: no I/O.
func worktreeDirName(dir string) string {
	i := strings.Index(dir, worktreePathMarker)
	if i < 0 {
		return ""
	}
	// Same e-NNN validation as selfDevProjectRoot: reject markers that don't
	// name a real task worktree (e.g. .endless/worktrees/scratch).
	if TaskIDFromWorktreePath(dir) == "" {
		return ""
	}
	name := dir[i+len(worktreePathMarker):]
	if j := strings.IndexByte(name, '/'); j >= 0 {
		name = name[:j]
	}
	return name
}

// SelfDetectWorktreeSandbox routes this process to the per-worktree sandbox
// (E-1281) when it runs inside a self-dev worktree that has a sandbox, unless
// an explicit DB context was already set. It is the E-1368 reversal of
// IsSandboxActive: instead of detecting that a wrapper routed us into a
// sandbox via XDG_CONFIG_HOME, the binary routes itself from cwd — replacing
// the bin-sandbox/ wrapper scripts entirely.
//
// The routing is cwd-derived and recomputed every invocation (never an
// inherited env var), so it satisfies the E-1429 gate the same way the
// --config-dir flag does: per-invocation and tied to physical location, not a
// sticky export that silently misroutes later commands. Explicit --config-dir
// (ConsumeDBContextFlag) is consumed first and wins via the dbContextDir guard
// below; the hook/tmux PinMainDB override still moves the DB to main
// afterward, with ConfigDir() (config.json, logs) following the self-detected
// sandbox per the E-1450 split.
//
// No-op unless ALL hold: cwd is inside <root>/.endless/worktrees/e-NNN,
// <root> is a self_dev project, and the sandbox config dir already exists on
// disk. The existence check is essential — routing to a missing sandbox would
// open a fresh empty DB at a half-built path. Must run before the first
// DB()/DBPath()/ConfigDir() use.
func SelfDetectWorktreeSandbox() {
	if dbContextDir != "" {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	root := selfDevProjectRoot(cwd)
	if root == "" || !projectIsSelfDev(root) {
		return
	}
	name := worktreeDirName(cwd)
	if name == "" {
		return
	}
	sandboxDir := filepath.Join(CacheDir(), "sandboxes", name, "endless")
	if info, err := os.Stat(sandboxDir); err != nil || !info.IsDir() {
		return
	}
	// setDetectedContextDir (not SetDBContextDir): a cwd-detected sandbox routes
	// config/logs to the sandbox but must NOT suppress the hook/tmux main
	// pin — session/pane state belongs in the main database (E-1450/E-1700).
	setDetectedContextDir(sandboxDir)
}

// projectIsSelfDev reports whether <root>/.endless/config.json has
// "self_dev": true. Mirrors the Python config.project_is_self_dev. A missing
// or unreadable config (or the flag unset) is false, so non-self-dev projects
// never trip the gate.
func projectIsSelfDev(root string) bool {
	data, err := os.ReadFile(filepath.Join(root, ".endless", "config.json"))
	if err != nil {
		return false
	}
	var cfg struct {
		SelfDev bool `json:"self_dev"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false
	}
	return cfg.SelfDev
}

// ProjectIsSelfDev reports whether <root>/.endless/config.json sets
// "self_dev": true. Exported wrapper over projectIsSelfDev for callers
// outside the monitor package (e.g. templatecmd).
func ProjectIsSelfDev(root string) bool { return projectIsSelfDev(root) }

// MinimizerConfig is a project's answer about the minimizer: whether the Stop
// gate is live, and whether the autoresearch loop may tune the prompt behind it.
//
// Two switches rather than one because they fail differently. `enabled` off
// means the channel is not there — nothing routes a reply through the minimizer
// and nothing holds the turn. `optimizer` off means the channel IS there but
// frozen: the shipped prompt runs, no variants are generated, nothing is
// promoted. A project that wants the gate without the research bill needs that
// second switch, and folding them into one boolean would make wanting it
// unexpressible.
type MinimizerConfig struct {
	Enabled   bool
	Optimizer bool
}

// MinimizerEnabled reports whether the minimizer's Stop gate is live for the
// project rooted at root (E-1953, reshaped by E-1975). Reads "minimizer" from
// <root>/.endless/config.json — falling back to the old scalar "report_gate" —
// and DEFAULTS TO TRUE: absent file, absent key, unreadable, or malformed all
// mean enabled.
//
// Default-on is the product decision, not a fallback: the pain of an ungated
// session is what the gate exists to remove, so shipping it inert would ship
// nothing. Only an explicit false turns it off, which is why the pointers in
// readMinimizer are required — with plain bools, "absent" and "false" collapse
// into the same zero value and every project would ship ungated while appearing
// to be configured.
//
// The switch lives in .endless/config.json and NOT in .claude/settings.json,
// and that is a security property rather than a filing preference: a gate an
// agent can switch off is not a gate, and agents edit .claude/settings.json in
// the course of normal work. Nor is it a code constant — it has to be settable
// per project, so that Endless's own checkout could opt out while the minimizer
// prompt was being tuned by hand without every other project shipping ungated
// too. (That exemption lapsed with E-1975: tuning is automated now, so the
// checkout that hosts the loop is the one place it must run.)
func MinimizerEnabled(root string) bool {
	cfg, _ := readMinimizer(root)
	return cfg.Enabled
}

// MinimizerOptimizerEnabled reports whether the autoresearch loop may generate,
// replay and promote variants for the project rooted at root.
func MinimizerOptimizerEnabled(root string) bool {
	cfg, _ := readMinimizer(root)
	return cfg.Optimizer
}

// readMinimizer reads the minimizer switches from <root>/.endless/config.json.
// declared is false when the file is absent, unreadable, malformed, or says
// nothing about the minimizer — which is what lets a caller distinguish "this
// layer says nothing" from "this layer says false" and fall through to another
// layer.
//
// Three accepted spellings, in precedence order:
//
//	"minimizer": {"enabled": true, "optimizer": false}   the current shape
//	"minimizer": false                                   shorthand for both off
//	"report_gate": false                                 E-1953's name
//
// The old scalar is still read because the rename must not silently re-enable a
// gate a project had switched off — a config change that turns enforcement back
// on by doing nothing is the one migration failure that costs the user something
// they cannot see. It sets `enabled` only; a project on the old key gets the
// optimizer default, which is the loop's own design (ED-1556: the user is a
// sensor, not a gate).
func readMinimizer(root string) (cfg MinimizerConfig, declared bool) {
	cfg = MinimizerConfig{Enabled: true, Optimizer: true}

	data, err := os.ReadFile(filepath.Join(root, ".endless", "config.json"))
	if err != nil {
		return cfg, false
	}
	var raw struct {
		Minimizer  json.RawMessage `json:"minimizer"`
		ReportGate *bool           `json:"report_gate"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return cfg, false
	}

	if len(raw.Minimizer) > 0 && string(raw.Minimizer) != "null" {
		var flag bool
		if err := json.Unmarshal(raw.Minimizer, &flag); err == nil {
			cfg.Enabled, cfg.Optimizer = flag, flag
			return cfg, true
		}
		var obj struct {
			Enabled   *bool `json:"enabled"`
			Optimizer *bool `json:"optimizer"`
		}
		if err := json.Unmarshal(raw.Minimizer, &obj); err == nil {
			if obj.Enabled != nil {
				cfg.Enabled = *obj.Enabled
			}
			if obj.Optimizer != nil {
				cfg.Optimizer = *obj.Optimizer
			}
			return cfg, true
		}
		return cfg, false
	}

	if raw.ReportGate != nil {
		cfg.Enabled = *raw.ReportGate
		return cfg, true
	}
	return cfg, false
}

// MinimizerConfigForCwd resolves the minimizer switches for a session working in
// cwd, preferring the nearest enclosing `.endless/config.json` and falling back
// to the registered project root.
//
// The cwd layer exists for worktrees. A task branch carries its own copy of
// `.endless/config.json`, so a branch that is CHANGING the switches — or any
// self-dev branch that must not be governed by the code it is still writing —
// can set them for its own sessions without touching the project root or every
// other worktree. Reading only the project root would mean the opt-out could not
// take effect until after the branch landed, which is exactly backwards.
//
// Precedence is nearest-wins, and only an explicit key counts. A worktree that
// says nothing inherits the project's answer rather than resetting it to the
// default, so removing the key from a branch does not silently re-enable a gate
// the project had switched off.
func MinimizerConfigForCwd(cwd, projectRoot string) MinimizerConfig {
	for dir := cwd; dir != ""; {
		if cfg, declared := readMinimizer(dir); declared {
			return cfg
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	cfg, _ := readMinimizer(projectRoot)
	return cfg
}

// MinimizerEnabledForCwd is the enabled half of MinimizerConfigForCwd, which is
// what every gate-side caller wants.
func MinimizerEnabledForCwd(cwd, projectRoot string) bool {
	return MinimizerConfigForCwd(cwd, projectRoot).Enabled
}

// InSelfDevWorktree reports whether the current working directory sits inside a
// task worktree of a self_dev project — the exact condition under which this
// process's DB context should resolve to the per-worktree sandbox rather than
// the real machine DB (E-1281).
//
// It exists so a surface that would otherwise pin the main DB unconditionally
// can honor the sandbox instead, keeping ONE self_dev rule for users to learn
// rather than a per-command exception (E-698). Best-effort: an unreadable cwd
// reports false, preserving the caller's prior behavior.
func InSelfDevWorktree() bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	root := selfDevProjectRoot(cwd)
	if root == "" {
		return false
	}
	return projectIsSelfDev(root)
}

// WorktreeHookBinary returns the endless-go binary a self_dev worktree's hook
// SHOULD run — <root>/.endless/worktrees/<name>/bin/endless-go — when dir is
// inside a self_dev worktree, or "" otherwise. The path is what the
// post-worktree-create provisioning copies and points .claude/settings.json's
// hooks block at (E-1662/E-998). Pure apart from the self_dev config read.
func WorktreeHookBinary(dir string) string {
	root := selfDevProjectRoot(dir)
	if root == "" || !projectIsSelfDev(root) {
		return ""
	}
	name := worktreeDirName(dir)
	if name == "" {
		return ""
	}
	return filepath.Join(root, ".endless", "worktrees", name, "bin", "endless-go")
}

// ForeignHookBuild reports whether an endless-go hook running as exePath from
// cwd is a FOREIGN build relative to the self_dev worktree containing cwd —
// i.e. cwd is inside a self_dev worktree but the running binary is not that
// worktree's own bin/endless-go (typically the global /usr/local/bin symlink →
// main's build). Returns the expected worktree binary path and true only in
// that mismatch; ("", false) when cwd is not in a self_dev worktree or the
// running binary already IS the worktree's. Both sides are resolved through
// symlinks, so a global symlink that points at the worktree binary is not
// flagged.
//
// The hook path WARNS (never refuses) on a true result: refusing would block
// every PostToolUse tool call, but a silent run of main's hook code in a
// self_dev session is exactly the degrade-silently bug this guards (E-1669).
// The sibling bare-shell DB-opening case refuses instead (E-1668).
func ForeignHookBuild(cwd, exePath string) (expected string, foreign bool) {
	expected = WorktreeHookBinary(cwd)
	if expected == "" {
		return "", false
	}
	if resolvedPath(expected) == resolvedPath(exePath) {
		return "", false
	}
	return expected, true
}

// resolvedPath returns p with symlinks resolved, or p unchanged if it can't be
// resolved (e.g. the worktree binary isn't built yet). Lets ForeignHookBuild
// compare a symlinked global against the real worktree binary fairly.
func resolvedPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// worktreeDBContextRefusal is the error returned by the gate. It is the
// backstop wording for direct Go-binary invocations; the Python CLI emits its
// own user-facing --db message (the locked text) before ever reaching here.
var worktreeDBContextRefusal = errors.New(
	"refusing to open the database: this process runs inside a self-dev " +
		"worktree but was given no explicit DB context. Invoke through the " +
		"endless CLI with --db main|sandbox, which threads --config-dir to " +
		"this binary.")

// guardWorktreeDBContext implements the E-1429 gate. When this process runs
// inside a self-dev worktree of a self_dev project and no explicit DB
// context was provided (flag or ForceRealDB), it refuses to open the DB.
// Bypass-proof: it sits at the single DB() entry point, so any binary that
// opens the DB is covered, including future ones, without an allowlist.
func guardWorktreeDBContext() error {
	if dbContextExplicit() {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		// Can't determine cwd; don't block (defensive — the cwd-less case is
		// not the worktree scenario this gate targets).
		return nil
	}
	root := selfDevProjectRoot(cwd)
	if root == "" || !projectIsSelfDev(root) {
		return nil
	}
	return worktreeDBContextRefusal
}

// DB returns a connection to the Endless SQLite database.
func DB() (*sql.DB, error) {
	if err := guardWorktreeDBContext(); err != nil {
		return nil, err
	}
	dbOnce.Do(func() {
		path := DBPath()
		dbConn, dbErr = sql.Open("sqlite", path)
		if dbErr != nil {
			dbErr = fmt.Errorf("opening database %s: %w", path, dbErr)
			return
		}
		// Verify the connection actually works (sql.Open may succeed lazily)
		if err := dbConn.Ping(); err != nil {
			dbErr = fmt.Errorf("connecting to database %s: %w", path, err)
			dbConn = nil
			return
		}
		// SQLite is single-writer; one connection ensures BEGIN IMMEDIATE
		// works correctly with Go's connection pool.
		dbConn.SetMaxOpenConns(1)
		if _, err := dbConn.Exec("PRAGMA journal_mode=WAL"); err != nil {
			log.Printf("endless-monitor: PRAGMA journal_mode=WAL: %v", err)
		}
		if _, err := dbConn.Exec("PRAGMA busy_timeout=5000"); err != nil {
			log.Printf("endless-monitor: PRAGMA busy_timeout=5000: %v", err)
		}
		if _, err := dbConn.Exec("PRAGMA foreign_keys=ON"); err != nil {
			log.Printf("endless-monitor: PRAGMA foreign_keys=ON: %v", err)
		}
		// E-1818: when pinned onto a real DB this binary does not own
		// (ForceRealDB / PinMainDB), open schema-passive — skip the schema.SQL
		// exec and every enum integrity gate below. An unlanded worktree binary
		// must not migrate, reseed, or fail-close a real DB the deployed binary
		// owns; only DATA writes (session/pane state) reach it. See
		// pinnedToForeignRealDB() for the full invariant. The per-connection
		// PRAGMAs above stay on both paths — they configure the connection, they
		// do not mutate schema.
		if !pinnedToForeignRealDB() {
			// schema.SQL is the authoritative schema, all CREATE ... IF NOT EXISTS:
			// it creates every table on a fresh DB and is a no-op on a populated
			// one. Destructive, one-off changes are applied separately at land
			// time via `endless db apply-change`, not here.
			if _, err := dbConn.Exec(schema.SQL); err != nil {
				dbErr = fmt.Errorf("applying schema to %s: %w", path, err)
				dbConn = nil
				return
			}
			// E-1538: enum/table integrity check. task_types is seeded by
			// schema.SQL on every connection; if a row is missing or drifted from
			// the Go TaskType enum we fail closed, since downstream INSERTs would
			// either violate the FK or write an id that has no enum constant.
			// Skipped on populated DBs that have not yet had the E-1538 migration
			// applied (the table will not exist; the migration creates it).
			if hasTable(dbConn, "task_types") {
				if err := tasktype.VerifyIntegrity(dbConn); err != nil {
					dbErr = fmt.Errorf("task_types integrity check on %s: %w", path, err)
					dbConn = nil
					return
				}
			}
			// E-1898: same fail-closed contract for the process_kinds enum mirror.
			// Skipped on populated DBs that have not yet had the E-1898 migration
			// applied (the table will not exist; the migration creates it).
			if hasTable(dbConn, "process_kinds") {
				if err := processkind.VerifyIntegrity(dbConn); err != nil {
					dbErr = fmt.Errorf("process_kinds integrity check on %s: %w", path, err)
					dbConn = nil
					return
				}
			}
			// E-1542: same fail-closed contract for the gate_kinds enum mirror.
			// Skipped on populated DBs that have not yet had the E-1542 migration
			// applied (the table will not exist; the migration creates it).
			if hasTable(dbConn, "gate_kinds") {
				if err := gatekind.VerifyIntegrity(dbConn); err != nil {
					dbErr = fmt.Errorf("gate_kinds integrity check on %s: %w", path, err)
					dbConn = nil
					return
				}
			}
			// E-1462: same fail-closed contract for the session_task_relations enum
			// mirror. Skipped on populated DBs that have not yet had the E-1462
			// migration applied (the table will not exist; the migration creates it).
			if hasTable(dbConn, "session_task_relations") {
				if err := sessiontaskrelation.VerifyIntegrity(dbConn); err != nil {
					dbErr = fmt.Errorf("session_task_relations integrity check on %s: %w", path, err)
					dbConn = nil
					return
				}
			}
		}
	})
	return dbConn, dbErr
}

func hasTable(db *sql.DB, table string) bool {
	var count int
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
	return count > 0
}

// BackupResult reports what BackupDB did, so a caller can name the file rather
// than claim a backup happened and leave the user to guess where (E-1942:
// `endless db backup` printed a bare "Database backed up.", which is unusable
// as the first half of a restore).
type BackupResult struct {
	// Path is the backup written, or — when Skipped — the recent backup that
	// made writing another unnecessary.
	Path string
	// Skipped is true when a backup newer than the throttle window already
	// existed, so nothing was written this call.
	Skipped bool
	// Pruned is how many aged-out backups the retention sweep removed.
	Pruned int
}

// backupThrottle is the minimum gap between two written backups. It is a
// THROTTLE, not a cadence: it stops two migrations a few seconds apart from
// writing two near-identical copies, and it has never made a backup happen.
//
// Cadence is E-2121's job (internal/backupjob), which fires this hourly. The
// distinction matters because the docstring here used to read "if last backup is
// > 60 seconds old", which sounds like a frequency — and on the strength of that
// reading nobody noticed the newest backup was nine days old.
const backupThrottle = 60 * time.Second

// BackupDB writes a consistent copy of the database into the backups directory
// and enforces the retention policy over what is already there.
//
// It writes nothing when a backup younger than backupThrottle already exists,
// reporting that one instead (BackupResult.Skipped). Retention is applied on
// both paths: it is an invariant of the directory, not a side effect of writing,
// so it holds even on a machine where the scheduled job never runs.
//
// The returned error covers BOTH halves, and a caller that cares which half
// failed reads BackupResult.Path: it is empty only when no backup exists, so a
// non-nil error with a non-empty Path means the copy is on disk and RETENTION is
// what did not complete. `endless db backup` uses exactly that to warn without
// failing a land; the job (internal/backupjob) does not distinguish, because a
// backups directory that has stopped being pruned is a job failure.
//
// Errors are returned rather than only logged: the CLI surfaces them, and the
// prompt hook logs them and carries on.
func BackupDB() (BackupResult, error) {
	return BackupDBContext(context.Background())
}

// BackupDBContext is BackupDB bounded by ctx. VACUUM INTO is the one step here
// that can run long — it rewrites the whole database — and internal/jobs hands
// every job a context carrying its lease deadline precisely so a slow step
// cannot outrun the claim protecting it from concurrent execution.
func BackupDBContext(ctx context.Context) (BackupResult, error) {
	src := DBPath()
	if _, err := os.Stat(src); err != nil {
		return BackupResult{}, fmt.Errorf("no database at %s: %w", src, err)
	}

	// Backups follow the DB: when ForceRealDB() has redirected DBPath() to the
	// real database, its backups land beside it rather than in the sandbox
	// (E-1450). In the normal case DBPath() is ConfigDir()/endless.db, so this
	// resolves to ConfigDir()/backups exactly as before.
	backupDir := backupsDir()
	os.MkdirAll(backupDir, 0755)

	// The throttle reads the newest backup's own TIMESTAMP, taken from its name.
	// The list position of an os.ReadDir entry is not that: the directory holds
	// a pre-restore parking file or an operator's stray copy often enough, and
	// whichever name happened to sort last used to decide whether a backup was
	// due — by its mtime, which a copy rewrites.
	existing, err := listBackups(backupDir)
	if err != nil {
		return BackupResult{}, err
	}
	if newest, ok := newestBackup(existing); ok && time.Since(newest.Stamp) < backupThrottle {
		// A recent backup exists. Name it: the caller reports a path either
		// way, and reporting the one that already covers this moment is
		// both true and the path a restore would use.
		pruned, pruneErr := pruneBackups(backupDir, time.Now())
		return BackupResult{
			Path:    filepath.Join(backupDir, newest.Name),
			Skipped: true,
			Pruned:  pruned,
		}, pruneErr
	}

	// Use SQLite VACUUM INTO for a consistent backup
	ts := time.Now().Format(backupStampLayout)
	dst := filepath.Join(backupDir, backupPrefix+ts+backupSuffix)

	backupDB, err := sql.Open("sqlite", src)
	if err != nil {
		return BackupResult{}, fmt.Errorf("open %s: %w", src, err)
	}
	defer backupDB.Close()

	// Match the main DB connection's busy_timeout so VACUUM INTO waits for
	// concurrent writers instead of failing immediately with SQLITE_BUSY.
	if _, err := backupDB.ExecContext(ctx, "PRAGMA busy_timeout=5000"); err != nil {
		log.Printf("backup PRAGMA busy_timeout=5000: %v", err)
	}

	_, err = backupDB.ExecContext(ctx, "VACUUM INTO ?", dst)
	if err != nil {
		return BackupResult{}, fmt.Errorf("VACUUM INTO %s: %w", dst, err)
	}

	pruned, pruneErr := pruneBackups(backupDir, time.Now())
	return BackupResult{Path: dst, Pruned: pruned}, pruneErr
}

// ProjectPath returns the registered filesystem path for a project ID in
// RESOLVED form, so callers can hand it straight to os.Stat, filepath.Join or
// git and compare it against a path the Python CLI computed for the same
// project. Every caller here treats the result as a real directory — worktree
// roots, lock files, the claim handoff's cd line — which is precisely why this
// one returns the resolved form and never the stored one (E-2011): the column
// now normally holds `~/...`, and a tilde is not a path in Go.
//
// The Python read sides (task_cmd, worktree_cmd, decision_cmd, matchers) call
// endless.project_path.resolved on the same column, so anything less here is a
// string the two halves disagree about (E-2002).
func ProjectPath(id int64) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}
	var path string
	err = db.QueryRow("SELECT path FROM projects WHERE id = ?", id).Scan(&path)
	if err != nil {
		return "", err
	}
	return ResolvedProjectPath(path)
}

// ProjectIDForPath looks up a registered project by working directory.
// Checks the exact path first, then walks up parent directories.
// Returns (id, true) if found, or creates/finds an anonymous project
// and returns (id, false) if the directory is not registered.
//
// dir is normalized first (E-2002): the Python CLI stores a canonical path, so
// comparing a raw cwd against it misses on every project reached through a
// symlink and auto-registers a duplicate. The walk climbs RESOLVED ancestors —
// the only form `filepath.Dir` can climb — and queries each one in STORED form,
// which is what the indexed column holds (E-2011). Home is looked up once for
// the whole walk rather than per rung.
//
// Only when the whole walk misses does projectIDForResolvedPath scan for a row
// written in an older spelling.
func ProjectIDForPath(dir string) (int64, bool, error) {
	db, err := DB()
	if err != nil {
		return 0, false, err
	}

	dir, err = ResolvedProjectPath(dir)
	if err != nil {
		return 0, false, err
	}
	home, err := resolvedHomeDir()
	if err != nil {
		return 0, false, err
	}

	// Walk up looking for a registered project
	check := dir
	for {
		var id int64
		err = db.QueryRow(
			"SELECT id FROM projects WHERE path = ?", homeRelative(check, home),
		).Scan(&id)
		if err == nil {
			return id, true, nil
		}

		parent := filepath.Dir(check)
		if parent == check {
			break
		}
		check = parent
	}

	// Nothing stored in canonical form matched — a row may predate E-2011 or
	// E-2002 and hold another spelling that denotes this directory anyway.
	id, found, err := projectIDForResolvedPath(db, dir)
	if err != nil {
		return 0, false, err
	}
	if found {
		return id, true, nil
	}

	// No registered project found — auto-register as active
	id, err = ensureAutoRegisteredProject(db, dir)
	if err != nil {
		return 0, false, err
	}
	return id, false, nil
}

// ensureAutoRegisteredProject auto-registers an unregistered directory
// as an active project. Uses the directory basename as the project name.
//
// dir arrives RESOLVED; the row is written in STORED form, the same shape
// `endless project register` writes, so the indexed lookup above matches it on
// the next event instead of falling through to the scan (E-2011).
func ensureAutoRegisteredProject(db *sql.DB, dir string) (int64, error) {
	stored, err := StoredProjectPath(dir)
	if err != nil {
		return 0, err
	}

	// Check if already exists at this path
	var id int64
	err = db.QueryRow(
		"SELECT id FROM projects WHERE path = ?",
		stored,
	).Scan(&id)
	if err == nil {
		return id, nil
	}

	// Auto-register with directory basename as name
	name := filepath.Base(dir)
	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	// Ensure unique name
	base := name
	for i := 2; ; i++ {
		var exists int
		db.QueryRow(
			"SELECT count(*) FROM projects WHERE name = ?", name,
		).Scan(&exists)
		if exists == 0 {
			break
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}

	result, err := db.Exec(
		"INSERT INTO projects (name, path, status, created_at, updated_at) "+
			"VALUES (?, ?, 'active', ?, ?)",
		name, stored, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("auto-registering project %s at %s: %w", name, stored, err)
	}

	log.Printf("auto-registered project: %s at %s", name, stored)
	return result.LastInsertId()
}

// ErrNoProjectContext is returned by ProjectRootFromCwd when no ancestor of
// the working directory contains a .endless/ directory. Callers wrap it with
// a message naming the operation that needed the project context.
var ErrNoProjectContext = errors.New("no project context: no ancestor directory contains .endless/")

// ProjectRootFromCwd walks up from the working directory and returns the
// first ancestor containing a .endless/ directory — the project root. Shared
// by every command that must resolve a project from cwd rather than from an
// explicit --project name (templatecmd, outputstylecmd).
//
// Returns the RESOLVED form (E-2002/E-2011): this walks the filesystem with
// os.Stat and the caller uses the answer as a directory, so it is deliberately
// not the tilde-prefixed form the projects column holds.
func ProjectRootFromCwd() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir, err := ResolvedProjectPath(cwd)
	if err != nil {
		return "", err
	}
	for {
		if st, err := os.Stat(filepath.Join(dir, ".endless")); err == nil && st.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNoProjectContext
		}
		dir = parent
	}
}
