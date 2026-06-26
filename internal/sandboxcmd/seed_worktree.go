package sandboxcmd

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/schema"

	_ "modernc.org/sqlite"
)

// nullSessionID is the designated non-Claude session_id. Used by init --mode
// worktree when CLAUDE_CODE_SESSION_ID is not set, so bare-terminal
// invocations inside the worktree attach to a real sessions row instead of
// hitting the "Cannot determine the Endless session for this pane" guard.
//
// Pending a formal `endless decision add` record after E-1507 lands.
const nullSessionID = "00000000-0000-0000-0000-000000000000"

// seedFromWorktree populates the sandbox DB at sandboxDir with the project +
// session rows the CLI needs on first use. cwd must be inside a git worktree
// of a registered project; the project row is copied from the main DB at
// ~/.config/endless/endless.db.
func seedFromWorktree(sandboxDir string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	mainCheckout, err := mainCheckoutFromWorktree(cwd)
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}
	mainDBPath := filepath.Join(home, ".config", "endless", "endless.db")
	proj, err := readMainProjectRow(mainDBPath, mainCheckout)
	if err != nil {
		return err
	}

	sandboxDBPath := filepath.Join(sandboxDir, "endless", "endless.db")
	if err := os.MkdirAll(filepath.Dir(sandboxDBPath), 0o755); err != nil {
		return fmt.Errorf("creating sandbox endless dir: %w", err)
	}
	sandboxDB, err := sql.Open("sqlite", sandboxDBPath)
	if err != nil {
		return fmt.Errorf("opening sandbox DB %s: %w", sandboxDBPath, err)
	}
	defer sandboxDB.Close()

	if _, err := sandboxDB.Exec(schema.SQL); err != nil {
		return fmt.Errorf("applying schema to sandbox DB: %w", err)
	}

	res, err := sandboxDB.Exec(
		"INSERT INTO projects (name, label, path, group_name, description, status, language, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		proj.Name, proj.Label, proj.Path, proj.GroupName, proj.Description,
		proj.Status, proj.Language, proj.CreatedAt, proj.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting project row: %w", err)
	}
	projectID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("last insert id: %w", err)
	}

	sessionID := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if sessionID == "" {
		sessionID = nullSessionID
	}

	_, err = sandboxDB.Exec(
		"INSERT INTO sessions (session_id, project_id, platform, state, started_at, last_activity) "+
			"VALUES (?, ?, 'claude', 'working', strftime('%Y-%m-%dT%H:%M:%S', 'now'), strftime('%Y-%m-%dT%H:%M:%S', 'now'))",
		sessionID, projectID,
	)
	if err != nil {
		return fmt.Errorf("inserting session row: %w", err)
	}

	if err := writeSandboxConfig(sandboxDir); err != nil {
		return err
	}
	return nil
}

// sandboxConfig mirrors the Python CLI's DEFAULT_CONFIG (src/endless/config.py).
// A struct (not a map) keeps the JSON field order deterministic.
type sandboxConfig struct {
	Roots        []string `json:"roots"`
	ScanInterval int      `json:"scan_interval"`
	Ignore       []string `json:"ignore"`
}

// writeSandboxConfig provisions <sandbox>/endless/config.json with the default
// config so the Python CLI works under --db sandbox. event_bridge.py's
// _get_or_create_node_id() hard-requires config.json (it does not auto-create),
// and adds node_id lazily on first event. Generated locally — the sandbox never
// reads the main config at ~/.config/endless (E-1585). No-op-safe to overwrite:
// only written once at seed time.
func writeSandboxConfig(sandboxDir string) error {
	cfg := sandboxConfig{
		Roots:        []string{"~/Projects"},
		ScanInterval: 300,
		Ignore:       []string{},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling sandbox config: %w", err)
	}
	configPath := filepath.Join(sandboxDir, "endless", "config.json")
	if err := os.WriteFile(configPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing sandbox config %s: %w", configPath, err)
	}
	return nil
}

// mainCheckoutFromWorktree walks from a path inside a git worktree to the
// main checkout via the git-dir vs git-common-dir discriminator (documented
// in this project's CLAUDE.md and used in Go at internal/monitor/db.go).
func mainCheckoutFromWorktree(dir string) (string, error) {
	gitDir, err := runGit(dir, "rev-parse", "--git-dir")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-dir in %s: %w", dir, err)
	}
	commonDir, err := runGit(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-common-dir in %s: %w", dir, err)
	}
	gitDirAbs, err := absPath(dir, gitDir)
	if err != nil {
		return "", err
	}
	commonAbs, err := absPath(dir, commonDir)
	if err != nil {
		return "", err
	}
	if gitDirAbs == commonAbs {
		return "", fmt.Errorf("cwd %s is the main checkout (or not a worktree); --mode worktree requires a worktree", dir)
	}
	main := filepath.Dir(commonAbs)
	if _, err := os.Stat(main); err != nil {
		return "", fmt.Errorf("main checkout %s: %w", main, err)
	}
	return main, nil
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func absPath(base, p string) (string, error) {
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	return filepath.Abs(filepath.Join(base, p))
}

type projectRow struct {
	Name        string
	Label       sql.NullString
	Path        string
	GroupName   sql.NullString
	Description sql.NullString
	Status      string
	Language    sql.NullString
	CreatedAt   string
	UpdatedAt   string
}

func readMainProjectRow(dbPath, mainCheckout string) (*projectRow, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("main DB %s not found: %w (run `endless register %s` from the main checkout first)", dbPath, err, mainCheckout)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening main DB %s: %w", dbPath, err)
	}
	defer db.Close()

	var p projectRow
	err = db.QueryRow(
		"SELECT name, label, path, group_name, description, status, language, created_at, updated_at "+
			"FROM projects WHERE path = ?",
		mainCheckout,
	).Scan(&p.Name, &p.Label, &p.Path, &p.GroupName, &p.Description, &p.Status, &p.Language, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no project in main DB at path %s; run `endless register %s` from the main checkout first", mainCheckout, mainCheckout)
	}
	if err != nil {
		return nil, fmt.Errorf("reading project row: %w", err)
	}
	return &p, nil
}
