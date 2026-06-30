package monitor

import (
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

	"github.com/mikeschinkel/endless/internal/gatekind"
	"github.com/mikeschinkel/endless/internal/schema"
	"github.com/mikeschinkel/endless/internal/sessionkind"
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
)

// ConfigDir returns the Endless configuration directory. When an explicit DB
// context was provided (--config-dir, via ConsumeDBContextFlag), it wins over
// XDG_CONFIG_HOME so config.json and logs follow the same target as the DB.
func ConfigDir() string {
	if dbContextDir != "" {
		return dbContextDir
	}
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "endless")
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
	return filepath.Join(ConfigDir(), "endless.db")
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

// SetDBContextDir records an explicit DB/config directory for this process,
// satisfying the E-1429 self-dev-worktree gate. Called by ConsumeDBContextFlag
// when the Python CLI threads --config-dir to a Go subprocess.
func SetDBContextDir(dir string) {
	dbContextDir = dir
}

// PinMainDB unconditionally routes the DB (DBPath() and DB()) to the real
// database under ~/.config/endless and satisfies the E-1429 worktree gate.
//
// It differs from ForceRealDB in two ways that matter for binaries invoked
// outside a Claude session's env injection:
//   - Unconditional: ForceRealDB only redirects when IsSandboxActive() (i.e.
//     XDG_CONFIG_HOME points into a sandbox). endless-tmux is invoked by tmux
//     and endless-channel by the MCP host, where XDG may be unset; the
//     conditional check would miss and the gate would refuse them.
//   - DB-path only: ConfigDir() is left untouched, so config.json and logs
//     keep following XDG_CONFIG_HOME (the worktree's sandbox). Only the DB
//     itself moves to main, matching the E-1450 split — session/channel/pane
//     state is real-world activity and belongs in the real ledger.
//
// Used by the always-main infrastructure binaries (endless-channel,
// endless-tmux). Must precede the first DB()/DBPath() use. The hook keeps
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
	args := os.Args
	cleaned := make([]string, 0, len(args))
	if len(args) > 0 {
		cleaned = append(cleaned, args[0])
	}
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config-dir":
			if i+1 < len(args) {
				SetDBContextDir(args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--config-dir="):
			SetDBContextDir(strings.TrimPrefix(a, "--config-dir="))
		default:
			cleaned = append(cleaned, a)
		}
	}
	os.Args = cleaned
}

// dbContextExplicit reports whether this process was handed an explicit DB
// target: the --config-dir flag (dbContextDir) or the hook's ForceRealDB
// override (dbPathOverride). Either satisfies the self-dev-worktree gate.
func dbContextExplicit() bool {
	return dbContextDir != "" || dbPathOverride != ""
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
// below; the hook/channel/tmux PinMainDB override still moves the DB to main
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
	SetDBContextDir(sandboxDir)
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
		// E-1571: same fail-closed contract for the session_kinds enum mirror.
		// Skipped on populated DBs that have not yet had the E-1571 migration
		// applied (the table will not exist; the migration creates it).
		if hasTable(dbConn, "session_kinds") {
			if err := sessionkind.VerifyIntegrity(dbConn); err != nil {
				dbErr = fmt.Errorf("session_kinds integrity check on %s: %w", path, err)
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
	})
	return dbConn, dbErr
}

func hasTable(db *sql.DB, table string) bool {
	var count int
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
	return count > 0
}

// BackupDB copies the database file to the backups directory if the last
// backup is older than 60 seconds. Keeps the last 60 backups.
func BackupDB() {
	src := DBPath()
	if _, err := os.Stat(src); err != nil {
		return
	}

	// Backups follow the DB: when ForceRealDB() has redirected DBPath() to the
	// real database, its backups land beside it rather than in the sandbox
	// (E-1450). In the normal case DBPath() is ConfigDir()/endless.db, so this
	// resolves to ConfigDir()/backups exactly as before.
	backupDir := filepath.Join(filepath.Dir(DBPath()), "backups")
	os.MkdirAll(backupDir, 0755)

	// Check if backup is needed (last backup > 60s ago)
	entries, _ := os.ReadDir(backupDir)
	if len(entries) > 0 {
		newest := entries[len(entries)-1]
		info, err := newest.Info()
		if err == nil && time.Since(info.ModTime()) < 60*time.Second {
			return // recent backup exists
		}
	}

	// Use SQLite VACUUM INTO for a consistent backup
	ts := time.Now().Format("20060102-150405")
	dst := filepath.Join(backupDir, fmt.Sprintf("endless-%s.db", ts))

	backupDB, err := sql.Open("sqlite", src)
	if err != nil {
		return
	}
	defer backupDB.Close()

	// Match the main DB connection's busy_timeout so VACUUM INTO waits for
	// concurrent writers instead of failing immediately with SQLITE_BUSY.
	if _, err := backupDB.Exec("PRAGMA busy_timeout=5000"); err != nil {
		log.Printf("backup PRAGMA busy_timeout=5000: %v", err)
	}

	_, err = backupDB.Exec("VACUUM INTO ?", dst)
	if err != nil {
		log.Printf("backup failed: %v", err)
		return
	}

	// Rotate: keep last 60 backups
	entries, _ = os.ReadDir(backupDir)
	if len(entries) > 60 {
		for _, e := range entries[:len(entries)-60] {
			os.Remove(filepath.Join(backupDir, e.Name()))
		}
	}
}

// ProjectPath returns the registered filesystem path for a project ID.
// Path is returned with ~ expansion applied so callers can use it directly.
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
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	return path, nil
}

// ProjectIDForPath looks up a registered project by working directory.
// Checks the exact path first, then walks up parent directories.
// Returns (id, true) if found, or creates/finds an anonymous project
// and returns (id, false) if the directory is not registered.
func ProjectIDForPath(dir string) (int64, bool, error) {
	db, err := DB()
	if err != nil {
		return 0, false, err
	}

	dir, err = filepath.Abs(dir)
	if err != nil {
		return 0, false, err
	}

	// Walk up looking for a registered project
	check := dir
	for {
		var id int64
		err := db.QueryRow(
			"SELECT id FROM projects WHERE path = ?", check,
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

	// No registered project found — auto-register as active
	id, err := ensureAutoRegisteredProject(db, dir)
	if err != nil {
		return 0, false, err
	}
	return id, false, nil
}

// ensureAutoRegisteredProject auto-registers an unregistered directory
// as an active project. Uses the directory basename as the project name.
func ensureAutoRegisteredProject(db *sql.DB, dir string) (int64, error) {
	// Check if already exists at this path
	var id int64
	err := db.QueryRow(
		"SELECT id FROM projects WHERE path = ?",
		dir,
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
		name, dir, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("auto-registering project %s at %s: %w", name, dir, err)
	}

	log.Printf("auto-registered project: %s at %s", name, dir)
	return result.LastInsertId()
}
