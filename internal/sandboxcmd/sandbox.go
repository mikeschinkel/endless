package sandboxcmd

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/monitor"
)

type sandboxMode string

const (
	modeEphemeral  sandboxMode = "ephemeral"
	modeKeep       sandboxMode = "keep"
	modePersistent sandboxMode = "persistent"
)

const metaFilename = ".sandbox-meta.json"

// Sandbox represents a provisioned sandbox directory.
type Sandbox struct {
	Name string
	Dir  string
	Meta SandboxMeta
	root *os.Root
}

// SandboxMeta is persisted at <dir>/.sandbox-meta.json.
type SandboxMeta struct {
	CreatedAt  time.Time   `json:"created_at"`
	Mode       sandboxMode `json:"mode"`
	CreatorPID int         `json:"creator_pid"`
	Name       string      `json:"name"`
}

func sandboxesDir() string {
	return filepath.Join(monitor.CacheDir(), "sandboxes")
}

func generateName() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// validateName rejects names that would escape sandboxesDir() or break the
// filesystem: empty, "." / "..", anything containing path separators.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("sandbox name cannot be empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("invalid sandbox name %q", name)
	}
	if name != filepath.Base(name) {
		return fmt.Errorf("sandbox name %q must not contain path separators", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("sandbox name %q must not contain path separators", name)
	}
	return nil
}

// Provision creates a new sandbox. If name is empty, a random hex name is
// generated. Errors if the named sandbox already exists.
func Provision(name string, mode sandboxMode) (*Sandbox, error) {
	if name != "" {
		if err := validateName(name); err != nil {
			return nil, err
		}
	}
	if name == "" {
		n, err := generateName()
		if err != nil {
			return nil, fmt.Errorf("generating sandbox name: %w", err)
		}
		name = n
	}
	parent := sandboxesDir()
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("creating parent dir %s: %w", parent, err)
	}
	dir := filepath.Join(parent, name)
	if _, err := os.Stat(dir); err == nil {
		// Distinguish a real sandbox (has meta) from a stale fragment.
		if _, mErr := os.Stat(filepath.Join(dir, metaFilename)); mErr == nil {
			return nil, fmt.Errorf("sandbox %q already exists; use 'endless-sandbox destroy %s' first", name, name)
		}
		return nil, fmt.Errorf("stale sandbox directory at %s (no %s — likely an interrupted enter); run 'endless-sandbox destroy %s' to remove it", dir, metaFilename, name)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("checking sandbox dir %s: %w", dir, err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating sandbox dir %s: %w", dir, err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("opening sandbox root %s: %w", dir, err)
	}

	sb := &Sandbox{
		Name: name,
		Dir:  dir,
		Meta: SandboxMeta{
			CreatedAt:  time.Now().UTC(),
			Mode:       mode,
			CreatorPID: os.Getpid(),
			Name:       name,
		},
		root: root,
	}

	if err := sb.writeMeta(); err != nil {
		root.Close()
		os.RemoveAll(dir)
		return nil, err
	}

	return sb, nil
}

func (sb *Sandbox) writeMeta() error {
	f, err := sb.root.Create(metaFilename)
	if err != nil {
		return fmt.Errorf("creating meta file: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(sb.Meta); err != nil {
		return fmt.Errorf("writing meta: %w", err)
	}
	return nil
}

// Load reads an existing sandbox by name and opens its Root.
func Load(name string) (*Sandbox, error) {
	dir := filepath.Join(sandboxesDir(), name)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("sandbox %q does not exist", name)
		}
		return nil, fmt.Errorf("checking sandbox dir %s: %w", dir, err)
	}
	meta, err := readMeta(dir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening sandbox root %s: %w", dir, err)
	}
	return &Sandbox{
		Name: name,
		Dir:  dir,
		Meta: meta,
		root: root,
	}, nil
}

func readMeta(dir string) (SandboxMeta, error) {
	var meta SandboxMeta
	data, err := os.ReadFile(filepath.Join(dir, metaFilename))
	if err != nil {
		return meta, fmt.Errorf("reading meta in %s: %w", dir, err)
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("parsing meta in %s: %w", dir, err)
	}
	return meta, nil
}

// Root returns the sandbox's *os.Root for in-sandbox file operations.
func (sb *Sandbox) Root() *os.Root {
	return sb.root
}

// Destroy closes the Root and removes the sandbox dir.
func (sb *Sandbox) Destroy() error {
	if sb.root != nil {
		sb.root.Close()
		sb.root = nil
	}
	if err := os.RemoveAll(sb.Dir); err != nil {
		return fmt.Errorf("removing sandbox dir %s: %w", sb.Dir, err)
	}
	return nil
}

// Env returns env vars to inject into a child process or subshell.
func (sb *Sandbox) Env() []string {
	return []string{
		"XDG_CONFIG_HOME=" + sb.Dir,
		"ENDLESS_SANDBOX=" + sb.Dir,
	}
}
