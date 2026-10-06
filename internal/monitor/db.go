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
	"github.com/mikeschinkel/endless/internal/rating"
	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

var (
	dbOnce = &sync.Once{}
	dbConn *sql.DB
	dbErr  error

	// dbPathOverride, when set, forces DBPath() to the main database regardless
	// of any other routing. Set once by PinMainDB() at process entry.
	dbPathOverride string

	// dbContextDir, when set, pins ConfigDir() (and therefore DBPath()) to an
	// explicit directory for the lifetime of this process. It is the E-1429
	// "explicit DB context", and since E-1668 a FLAG is the only thing that
	// sets it: --db main|sandbox, or the --db-dir escape (ConsumeDBFlags).
	// Inside a self-dev worktree, guardWorktreeDBContext() refuses to open the
	// DB unless an explicit context exists (this var or dbPathOverride).
	//
	// Deliberately NOT satisfied by XDG_CONFIG_HOME: an env var can be
	// exported once and silently route every later command to the wrong DB --
	// the exact failure mode the gate exists to kill. Nor by cwd, which E-1368
	// briefly allowed and E-1668 removed: cwd is stickier than an env var,
	// since it needs no export at all. Only a per-invocation flag (or the
	// hook/tmux PinMainDB) counts as explicit.
	//
	// The principle, stated once so it is not re-derived: DETECTION DECIDES
	// WHERE TO LOOK; ONLY A FLAG DECIDES THAT YOU MAY OPEN IT. `--db sandbox`
	// still reads cwd, because cwd is what says WHICH worktree's sandbox --
	// the flag grants the permission, cwd supplies the address.
	dbContextDir string
)

// ConfigDir returns the Endless configuration directory. When an explicit DB
// context was provided (--db/--db-dir, via ConsumeDBFlags), it wins over the
// default (main) so config.json and logs follow the same target as the DB.
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
// through a per-worktree sandbox (E-1281) — since E-2186, only ever because
// `--db sandbox` (or a --db-dir naming a sandbox) chose it. eventcmd uses it to
// keep the sandbox's ledger out of git. See E-1729.
//
// Detection asks the resolver rather than matching a path prefix (E-1964).
// Prefix-matching worked only while every sandbox sat under one root; a sandbox
// that lives inside its own worktree has no shared root to match against, and a
// project that moved its sandboxes out of tree has a root endless does not know
// until it reads that project's config.
//
// Two questions, because a sandbox path names its worktree in the normal layout
// and does not under an override:
//
//  1. Does ConfigDir() resolve to the sandbox of the worktree ConfigDir() is
//     itself inside? True for the in-tree layout, and needs nothing but the
//     path already in hand.
//  2. Failing that, does it resolve to the sandbox of the worktree CWD is
//     inside? The out-of-tree case, where the path names no worktree and only
//     cwd can say which worktree it belongs to.
func IsSandboxActive() bool {
	configDir := ConfigDir()
	if configDir == "" {
		return false
	}
	if sandbox := WorktreeSandboxConfigDir(configDir); sandbox != "" && sandbox == configDir {
		return true
	}
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	sandbox := WorktreeSandboxConfigDir(cwd)
	return sandbox != "" && sandbox == configDir
}

// DBPath returns the path to the Endless SQLite database.
func DBPath() string {
	if dbPathOverride != "" {
		return dbPathOverride
	}
	return string(dbcontext.DBPath(dt.DirPath(dbContextDir)))
}

// HasExplicitDBContext reports whether a per-invocation DB flag was consumed for
// this process. Callers that would otherwise PinMainDB use this to let an
// explicit target win — the E-1429 contract is that a flag is trustworthy and
// beats the env-driven main pin. Production invokers of the pinned surfaces (the
// Claude hook, tmux) never pass one, so this stays false there and the main pin
// still applies.
//
// Since E-1668 it is simply "is there a context at all": a flag is the only
// thing that can set dbContextDir, so there is no longer a guessed context to
// tell an explicit one apart from. The E-1700 routing this once protected —
// a self-dev worktree's own dev session writing session/pane state to MAIN
// rather than to its sandbox — is preserved by that same fact: such a session
// passes no flag, so the pin still runs.
func HasExplicitDBContext() bool {
	return dbContextDir != ""
}

// SetDBContextDir records an EXPLICIT DB/config directory for this process,
// satisfying the E-1429 self-dev-worktree gate and beating the hook/tmux main
// pin. Called by ConsumeDBFlags once it has resolved --db / --db-dir, and by
// tests that deliberately target a database.
func SetDBContextDir(dir string) {
	dbContextDir = dir
}

// mainConfigDir is the deployed installation's config directory — what `--db
// main` resolves to: $XDG_CONFIG_HOME/endless when the user set it, else
// $HOME/.config/endless (dbcontext, "Main is the default"). Following HOME is
// what lets a verify suite, which runs under a temp HOME and XDG_CONFIG_HOME,
// say `--db main` and mean its own isolated main rather than the developer's.
//
// Mirrors Python's config.main_config_dir, so "the main database" means one
// thing across both layers.
func mainConfigDir() (string, error) {
	dir, err := dbcontext.MainConfigDir()
	return string(dir), err
}

// sandboxConfigDirForCwd resolves `--db sandbox` to THIS worktree's sandbox
// config dir, or reports why it cannot.
//
// cwd is read here and that is not the E-1368 guess returning: the flag is what
// grants permission to open a database, and cwd only supplies the address of
// the one permitted. Refused outside a self-dev worktree for the same reason
// Python's config.apply_db_choice refuses it — there is no sandbox to name.
//
// The address comes from WorktreeSandboxConfigDir (E-1964), which composes it
// from the worktree rather than from the cache root, so this function no longer
// knows a sandbox layout — it knows only that the resolver has one.
//
// A missing sandbox is REFUSED, not created. Inventing one used to mean a stray
// database in the cache; now it would mean a fresh empty database inside the
// worktree, which is worse, because it looks exactly like the real thing. A
// sandbox is created with its worktree, so an absent one is an anomaly, and the
// refusal says so.
func sandboxConfigDirForCwd() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolving working directory for --db sandbox: %w", err)
	}
	root := selfDevProjectRoot(cwd)
	if root == "" || !projectIsSelfDev(root) {
		return "", errNotInSelfDevWorktree
	}
	sandbox := WorktreeSandboxDir(cwd)
	if sandbox == "" {
		return "", errNotInSelfDevWorktree
	}
	if !isDir(sandbox) {
		return "", sandboxMissingError(WorktreeRoot(cwd), sandbox)
	}
	return filepath.Join(sandbox, "endless"), nil
}

// errNotInSelfDevWorktree is the one refusal both of sandboxConfigDirForCwd's
// "there is no sandbox to name" branches return.
var errNotInSelfDevWorktree = errors.New(
	"--db sandbox only applies inside a self-dev worktree " +
		"(.endless/worktrees/e-NNN); cwd is not in one")

// PinMainDB unconditionally routes the DB (DBPath() and DB()) to the main
// database (dbcontext.MainDBPath) and satisfies the E-1429 worktree gate.
//
// DB-path only: ConfigDir() is left untouched. Session/pane state is
// real-world activity and belongs in the main database (E-1450), whatever the
// process's cwd.
//
// Used by the always-main surfaces (the hook, `endless-go tmux`,
// session-status, project-status). Must precede the first DB()/DBPath() use.
// Its sibling ForceRealDB — the conditional pin that escaped an injected
// XDG_CONFIG_HOME — went with that injection (E-2186).
func PinMainDB() {
	path, err := dbcontext.MainDBPath()
	if err != nil {
		return
	}
	dbPathOverride = string(path)
}

// ErrDBFlagConflict is returned when one invocation spells its DB choice twice.
//
// An ALIAS of the parser's own sentinel, not a second one: the check moved into
// internal/dbcontext with the parse, and callers that already test for this by
// identity must keep matching the error the parser actually returns.
var ErrDBFlagConflict = dbcontext.ErrDBFlagConflict

// ConsumeDBFlags strips this binary's DB-context flags out of os.Args (wherever
// they appear) and resolves them to a config directory. Called once at the top
// of main(), BEFORE os.Args[1] is read as the subcommand, so existing positional
// parsing is unaffected and `endless-go --db main event emit ...` works.
//
// The PARSE lives in internal/dbcontext (E-2157), which owns the vocabulary for
// every binary that has one; this is the routing layer on top. The split is not
// arbitrary — it falls exactly where cwd enters. dbcontext resolves `--db main`
// and `--db-dir` because they need nothing but $HOME and the argument itself.
// `--db sandbox` needs the working directory to say WHICH worktree is meant,
// dbcontext never reads it, so the choice arrives here as a word and this
// function is where it becomes a path.
//
//	--db main        the project's main database ($HOME/.config/endless)
//	--db sandbox     this worktree's sandbox database, addressed from cwd
//	--db-dir <path>  name a directory outright
//
// Reading cwd here is not the E-1368 guess returning: the flag is what grants
// permission to open a database, and cwd only supplies the address of the one
// permitted.
func ConsumeDBFlags() (err error) {
	var cleaned []string
	var flags dbcontext.Flags
	var dir dt.DirPath
	var sandbox string

	cleaned, flags, err = dbcontext.ConsumeFlags(os.Args)
	if err != nil {
		goto end
	}
	os.Args = cleaned

	switch flags.Choice {
	case dbcontext.ChoiceNone:
		goto end
	case dbcontext.ChoiceDir:
		dir = flags.Dir
	case dbcontext.ChoiceMain:
		dir, err = dbcontext.MainConfigDir()
		if err != nil {
			goto end
		}
	case dbcontext.ChoiceSandbox:
		sandbox, err = sandboxConfigDirForCwd()
		if err != nil {
			goto end
		}
		dir = dt.DirPath(sandbox)
	}
	SetDBContextDir(string(dir))

end:
	return err
}

// dbContextExplicit reports whether this process was handed an explicit DB
// target: a --db/--db-dir flag (dbContextDir) or the hook/tmux pin
// (dbPathOverride). Either satisfies the self-dev-worktree gate.
func dbContextExplicit() bool {
	return dbContextDir != "" || dbPathOverride != ""
}

// dbOpened records that this process actually opened the database. Set by DB()
// once, on the path where a connection exists.
var dbOpened bool

// DBOpened reports whether this process has opened the database (E-1668).
//
// It is what keeps the provenance trace honest: a subcommand that answered from
// git alone — `worktree ledger-orphans`, `session-query worktree-unsettled` —
// must not print a line naming a database it never consulted. Asked of the
// store rather than judged per command, so a verb that starts reading the
// database later begins announcing it without anyone remembering to.
func DBOpened() bool { return dbOpened }

// DBContextPinned reports whether this process's database was chosen IN CODE
// rather than by its caller — PinMainDB (the hook, tmux, session-status,
// project-status), which signals through dbPathOverride.
//
// It is E-1668's announce exemption. If the caller could not have influenced the
// choice there is nothing to disambiguate, and the tmux status line has no room
// for it besides. A pin is not a resolution.
func DBContextPinned() bool { return dbPathOverride != "" }

// candidateBuild reports whether this executable was built inside a task
// worktree, i.e. whether it is unlanded code.
//
// Keyed on the EXECUTABLE's path rather than cwd. cwd answers "where is the
// user working", which is a different question and the wrong one: the global
// binary invoked from inside a worktree is still the installed build and may
// open main, while the worktree's binary invoked from anywhere may not
// (ED-1601).
func candidateBuild() bool {
	return strings.Contains(executablePath(), worktreePathMarker)
}

// osExecutable is os.Executable, a variable so a test can play a worktree build
// without being one.
var osExecutable = os.Executable

// realDBPath is the deployed installation's database, independent of any routing
// in force. It hardcodes the same location PinMainDB does, so "the main database"
// means one thing across both.
func realDBPath() string {
	path, err := dbcontext.MainDBPath()
	if err != nil {
		return ""
	}
	return string(path)
}

// WorktreeBuildOnMainDB reports whether this process is a binary built inside a
// task worktree whose database context resolves to the MAIN database — the one
// pairing ED-1601 forbids, and which DB() therefore refuses before opening.
//
// Exported for the E-698 job runner, which reports the same state as its reason
// for not running rather than failing on the refused connect.
func WorktreeBuildOnMainDB() bool {
	return refusesMainDB(candidateBuild(), isMainDB(DBPath()))
}

// isMainDB reports whether path is the main database, comparing resolved forms
// so a symlinked config directory or an unclean --db-dir cannot slip past.
//
// "Main" is dbcontext.MainDBPath(), which follows $HOME: under the verify
// runner's temp HOME the throwaway database IS main, which is what lets a suite
// exercise the refusal at all.
func isMainDB(path string) bool {
	main := realDBPath()
	if main == "" || path == "" {
		return false
	}
	return resolvedPath(filepath.Clean(path)) == resolvedPath(filepath.Clean(main))
}

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

// projectIsSelfDev reports whether <root>/.endless/config.json has
// "self_dev": true. Mirrors the Python config.project_is_self_dev. A missing
// or unreadable config (or the flag unset) is false, so non-self-dev projects
// never trip the gate.
//
// Since ED-1554 this answers only "does endless route its OWN database into
// this project's sandboxes" — it no longer decides whether a sandbox exists,
// which is now true of every project.
func projectIsSelfDev(root string) bool {
	return readProjectConfig(root).SelfDev
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

// resolvedPath returns p with symlinks resolved, or p unchanged if it can't be
// resolved. Lets the foreign-real-DB checks compare a symlinked executable
// against a real path fairly.
func resolvedPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// worktreeDBContextRefusal is the error returned by the gate. It is the
// backstop wording for direct Go-binary invocations; the Python CLI emits its
// own user-facing --db message (the locked text) before ever reaching here.
//
// It names the flags THIS binary takes. It used to say the endless CLI "threads
// --config-dir to this binary", which stopped being true in E-1668 when
// endless-go took --db itself — and a refusal that names a remedy the reader
// cannot type is worse than terse.
var worktreeDBContextRefusal = errors.New(
	"refusing to open the database: this process runs inside a self-dev " +
		"worktree, where the project's main database and this worktree's " +
		"sandbox are both reachable, and nothing said which one to use. " +
		"Pass --db main or --db sandbox (or --db-dir <dir> to name one " +
		"outright).")

// guardWorktreeDBContext implements the E-1429 gate. When this process runs
// inside a self-dev worktree of a self_dev project and no explicit DB
// context was provided (a --db/--db-dir flag, or the hook/tmux pin), it refuses
// to open the DB. Bypass-proof: it sits at the single DB() entry point, so any
// binary that opens the DB is covered, including future ones, without an
// allowlist.
//
// E-1668 restored its bite. E-1368's cwd self-detect set dbContextDir, which
// satisfied dbContextExplicit() — so the gate went on passing while nothing had
// actually been said, and a foreign build run inside a worktree answered from
// that worktree's sandbox in silence. Detection is gone; the gate is unchanged.
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
	if sandbox := WorktreeSandboxDir(cwd); sandbox != "" && !isDir(sandbox) {
		return sandboxMissingError(WorktreeRoot(cwd), sandbox)
	}
	return worktreeDBContextRefusal
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// sandboxMissingError is the refusal for a self-dev worktree whose sandbox is
// not there (E-1964). It replaces the generic "no explicit DB context" wording
// for that one case, because the two have different remedies and only this one
// is an anomaly.
//
// Nothing rebuilds the sandbox — not here, not in the resolver, not on the next
// command. A sandbox is created with its worktree, so reaching this means
// something removed a directory endless expects to exist; a silent recovery
// would hide that and act on a state nobody has understood.
//
// Only reachable in a self-dev project, which is why the remedy can name that
// project's own recipe. Mirrors Python's config.sandbox_missing_refusal.
func sandboxMissingError(worktree, sandbox string) error {
	if worktree == "" {
		worktree = "(unknown)"
	}
	return fmt.Errorf(
		"refusing to open the database: this worktree has no sandbox.\n\n"+
			"  worktree: %s\n"+
			"  expected: %s\n\n"+
			"A sandbox is created with its worktree, so a missing one means something "+
			"removed it — worth understanding before carrying on. To recreate and "+
			"seed it, run this from the worktree:\n\n"+
			"    just dev-sandbox-init",
		worktree, sandbox)
}

// DB returns a connection to the Endless SQLite database.
//
// The connect does four things, in order, and the order is load-bearing:
//
//  1. The E-1429 worktree gate: inside a self-dev worktree, refuse unless a
//     flag (or the hook/tmux pin) said which database.
//  2. ED-1601: a binary built inside a task worktree never opens the main
//     database. Checked BEFORE the file is opened, so a refused binary cannot
//     have touched it — and outside every other branch, so it holds for the
//     hook's pinned path as much as for an explicit --db main.
//  3. The schema direction rule (E-2020, ED-1570): behind → back up and apply
//     forward; ahead → halt; equal → nothing to apply. See reconcileSchema.
//  4. Seed the enum mirrors and run the fail-closed integrity gates, on every
//     connect that got this far.
func DB() (*sql.DB, error) {
	if err := guardWorktreeDBContext(); err != nil {
		return nil, err
	}
	dbOnce.Do(func() {
		path := DBPath()
		if refusesMainDB(candidateBuild(), isMainDB(path)) {
			dbErr = worktreeBuildRefusal(path)
			return
		}
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
		// E-1668: the store has answered, so an answer may now name it.
		dbOpened = true
		// SQLite is single-writer; one connection ensures BEGIN IMMEDIATE
		// works correctly with Go's connection pool.
		dbConn.SetMaxOpenConns(1)
		if _, err := dbConn.Exec("PRAGMA journal_mode=WAL"); err != nil {
			log.Printf("endless-monitor: PRAGMA journal_mode=WAL: %v", err)
		}
		if _, err := dbConn.Exec("PRAGMA busy_timeout=5000"); err != nil {
			log.Printf("endless-monitor: PRAGMA busy_timeout=5000: %v", err)
		}
		// Foreign key enforcement is owned HERE now. schema.Migrate turns it on
		// too, but a connect no longer migrates, so this pragma is what keeps
		// every FK the schema declares armed on the ordinary path.
		if _, err := dbConn.Exec("PRAGMA foreign_keys=ON"); err != nil {
			log.Printf("endless-monitor: PRAGMA foreign_keys=ON: %v", err)
		}
		if err := reconcileSchema(dbConn, path); err != nil {
			dbErr = err
			if errors.Is(err, ErrSchemaRefused) {
				faultConn = dbConn
			}
			dbConn = nil
			return
		}
		// E-1538: enum/table integrity check. task_types is seeded by
		// seeds.sql on every connection; if a row is missing or drifted from
		// the Go TaskType enum we fail closed, since downstream INSERTs would
		// either violate the FK or write an id that has no enum constant.
		if hasTable(dbConn, "task_types") {
			if err := tasktype.VerifyIntegrity(dbConn); err != nil {
				dbErr = fmt.Errorf("task_types integrity check on %s: %w", path, err)
				dbConn = nil
				return
			}
		}
		// E-1898: same fail-closed contract for the process_kinds enum mirror.
		if hasTable(dbConn, "process_kinds") {
			if err := processkind.VerifyIntegrity(dbConn); err != nil {
				dbErr = fmt.Errorf("process_kinds integrity check on %s: %w", path, err)
				dbConn = nil
				return
			}
		}
		// E-1542: same fail-closed contract for the gate_kinds enum mirror.
		if hasTable(dbConn, "gate_kinds") {
			if err := gatekind.VerifyIntegrity(dbConn); err != nil {
				dbErr = fmt.Errorf("gate_kinds integrity check on %s: %w", path, err)
				dbConn = nil
				return
			}
		}
		// E-1462: same fail-closed contract for the session_task_relations enum
		// mirror.
		if hasTable(dbConn, "session_task_relations") {
			if err := sessiontaskrelation.VerifyIntegrity(dbConn); err != nil {
				dbErr = fmt.Errorf("session_task_relations integrity check on %s: %w", path, err)
				dbConn = nil
				return
			}
		}
		// E-1813: same fail-closed contract for the complexity_levels and
		// risk_levels rating mirrors. Both are created by the same
		// migration, so one probe covers the pair.
		if hasTable(dbConn, "complexity_levels") {
			if err := rating.VerifyIntegrity(dbConn); err != nil {
				dbErr = fmt.Errorf("rating levels integrity check on %s: %w", path, err)
				dbConn = nil
				return
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

	// Backups follow the DB: when PinMainDB() has redirected DBPath() to the
	// main database, its backups land beside it rather than in the sandbox
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
// The walk is ResolveDirectory's, which also stops at an ignored row or an
// IgnoreMarker: such a directory returns ErrIgnoredDirectory and is never
// auto-registered (E-2251).
func ProjectIDForPath(dir string) (int64, bool, error) {
	db, err := DB()
	if err != nil {
		return 0, false, err
	}

	dir, err = ResolvedProjectPath(dir)
	if err != nil {
		return 0, false, err
	}
	// The nearest registered or ignored rung decides (E-2251). An ignored
	// directory is never auto-registered: it is a directory the user said is
	// not a project, and the hook's job there is to record nothing.
	v, err := ResolveDirectory(db, dir)
	if err != nil {
		return 0, false, err
	}
	switch v.Kind {
	case VerdictProject:
		return v.ProjectID, true, nil
	case VerdictIgnored:
		return 0, false, fmt.Errorf("%w: %s (at %s)", ErrIgnoredDirectory, dir, v.At)
	}

	// No registered project found — auto-register as active
	id, err := ensureAutoRegisteredProject(db, dir)
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

// enclosingProjectRoot returns the project root that encloses dir, or "" when
// dir is inside no registered project.
//
// Inside a .endless/worktrees/e-NNN worktree that is the MAIN checkout above the
// worktree segment, not the worktree itself — a worktree is a checkout of the
// project, not a second project. Otherwise it is the nearest ancestor holding a
// .endless/config.json. Mirrors Python's config.enclosing_project_root, so the
// two layers answer "which project am I in" the same way.
func enclosingProjectRoot(dir string) string {
	if root := selfDevProjectRoot(dir); root != "" {
		return root
	}
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".endless", "config.json")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// DBProvenance describes the database that answered this process, for an output
// surface to state (E-1668): a short name a human reads, and the resolved
// directory, which is the unambiguous form.
//
// ok is false when there is nothing to announce, and the two cases are the rule
// rather than exceptions to it:
//
//   - The process never opened the database, so no answer came from one.
//   - The context was PINNED IN CODE (hook, tmux, the two status views). The
//     caller could not have influenced it, so there is nothing to disambiguate.
//   - The enclosing project is not self-dev, so it has exactly one database and
//     naming it says nothing that could have been otherwise.
//
// The name is derived from the directory that was RESOLVED rather than from the
// flag that was typed, so it describes what happened rather than what was asked
// for.
func DBProvenance() (name, dir string, ok bool) {
	if !dbOpened || DBContextPinned() {
		return "", "", false
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", false
	}
	root := enclosingProjectRoot(cwd)
	if root == "" || !projectIsSelfDev(root) {
		return "", "", false
	}
	dir = ConfigDir()
	if main, err := mainConfigDir(); err == nil && dir == main {
		return "main", dir, true
	}
	if n := worktreeDirName(cwd); n != "" {
		if dir == WorktreeSandboxConfigDir(cwd) {
			return "sandbox (" + n + ")", dir, true
		}
	}
	return dir, dir, true
}
