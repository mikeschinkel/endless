package settingscmd

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/go-cfgstore"
	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/settings"

	// Registrations the export is tested over, as endless-go imports them.
	_ "github.com/mikeschinkel/endless/internal/autospawnjob"
	_ "github.com/mikeschinkel/endless/internal/mainsyncjob"
	_ "github.com/mikeschinkel/endless/internal/monitor"
	_ "github.com/mikeschinkel/endless/internal/triagejob"
)

func TestMain(m *testing.M) {
	// go-cfgstore panics without a logger on the paths a refused file takes;
	// endless-go's main sets one first.
	cfgstore.SetLogger(slog.New(slog.DiscardHandler))
	if err := settings.Initialize(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// fixture writes a machine file and a project file under a temp HOME.
func fixture(t *testing.T, machine, project string) (proj dt.DirPath, machineFile, projectFile string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	mf, err := config.MachineFile()
	if err != nil {
		t.Fatal(err)
	}
	machineFile = string(mf)
	write(t, machineFile, machine)
	dir := t.TempDir()
	projectFile = string(config.ProjectFile(dt.DirPath(dir)))
	write(t, projectFile, project)
	return dt.DirPath(dir), machineFile, projectFile
}

func write(t *testing.T, file, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func entry(t *testing.T, exp Export, id string) Entry {
	t.Helper()
	for _, e := range exp.Settings {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("export has no %s", id)
	return Entry{}
}

// TestBuild_ValuesAndSources: the project file wins where a setting may live
// there; a machine-only key ignores the project file, as its reader does; a
// project-only key ignores the machine file; "unset" falls through to the
// default.
func TestBuild_ValuesAndSources(t *testing.T) {
	proj, _, _ := fixture(t,
		`{"main_sync":{"enabled":"on","interval":"2m"},"checks":{"session_audit":"on","task_required":"off"}}`,
		`{"main_sync":{"enabled":"off","interval":"9m"},"prime":{"enabled":"unset","cap":7},"checks":{"task_required":"on"}}`)
	exp, err := Build(proj)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, c := range []struct{ id, value, source string }{
		{"main_sync.enabled", "off", "project"},
		{"main_sync.interval", "2m", "machine"},
		{"prime.enabled", "off", "default"},
		{"prime.cap", "7", "project"},
		{"checks.session_audit", "on", "machine"},
		{"checks.task_required", "on", "project"},
		{"checks.bash_git_writes", "on", "default"},
		{"auto_spawn.placement", "last", "default"},
	} {
		e := entry(t, exp, c.id)
		if e.Value != c.value || e.Source != c.source {
			t.Errorf("%s = %s from %s, want %s from %s", c.id, e.Value, e.Source, c.value, c.source)
		}
	}
	e := entry(t, exp, "main_sync.enabled")
	if !e.Suggestable || e.Rationale == "" || e.Kind != "optin" || e.Files != "project" {
		t.Errorf("main_sync.enabled metadata = %+v", e)
	}
	if c := entry(t, exp, "auto_spawn.target").Choices; len(c) != 2 {
		t.Errorf("auto_spawn.target choices = %v", c)
	}
}

// TestBuild_RefusesALegacyBoolean names the file and the key.
func TestBuild_RefusesALegacyBoolean(t *testing.T) {
	proj, _, projectFile := fixture(t, `{}`, `{"main_sync":{"enabled":true}}`)
	_, err := Build(proj)
	bad := config.OptInErrors(err)
	if len(bad) != 1 || bad[0].File != projectFile || bad[0].Key != "main_sync.enabled" {
		t.Fatalf("Build error = %v, want one OptInError for main_sync.enabled in %s", err, projectFile)
	}
	if !errors.Is(err, settings.ErrLegacyBool) {
		t.Errorf("error does not wrap ErrLegacyBool: %v", err)
	}
	if msg := bad[0].Error(); !strings.Contains(msg, `write "on"`) {
		t.Errorf("message does not say what to write: %s", msg)
	}
}

// TestMigrateFile rewrites only the legacy booleans at opt-in keys and keeps
// every other byte — an `enabled` inside an array entry included.
func TestMigrateFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.json")
	in := `{
  "name": "p",
  "main_sync": {"enabled": true, "interval": "5m"},
  "prime":{"enabled":false},
  "checks": {
    "drift_detection": true,
    "session_audit": "on"
  },
  "matchers": [{"type": "pivot", "enabled": true}],
  "minimizer": {"enabled": false}
}
`
	want := `{
  "name": "p",
  "main_sync": {"enabled": "on", "interval": "5m"},
  "prime":{"enabled":"off"},
  "checks": {
    "drift_detection": "on",
    "session_audit": "on"
  },
  "matchers": [{"type": "pivot", "enabled": true}],
  "minimizer": {"enabled": false}
}
`
	write(t, file, in)
	rewrites, err := MigrateFile(dt.Filepath(file))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	if string(got) != want {
		t.Errorf("migrated file:\n%s\nwant:\n%s", got, want)
	}
	var keys []string
	for _, r := range rewrites {
		keys = append(keys, r.Key+"="+r.To)
	}
	if strings.Join(keys, " ") != "main_sync.enabled=on prime.enabled=off checks.drift_detection=on" {
		t.Errorf("rewrites = %v", keys)
	}

	// Idempotent: a second run finds nothing.
	rewrites, err = MigrateFile(dt.Filepath(file))
	if err != nil || len(rewrites) != 0 {
		t.Errorf("second migrate = %v, %v; want nothing", rewrites, err)
	}
}

func TestMigrateFile_MissingFile(t *testing.T) {
	_, err := MigrateFile(dt.Filepath(filepath.Join(t.TempDir(), "nope.json")))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("MigrateFile(missing) = %v, want ErrNotExist", err)
	}
}

// TestMetadata reads no file: a config that Build refuses does not stop it.
func TestMetadata(t *testing.T) {
	fixture(t, `{"checks":{"x":true}}`, `{}`)
	exp, err := Metadata()
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if e := entry(t, exp, "prime.enabled"); e.Value != "off" || e.Source != "default" {
		t.Errorf("prime.enabled = %+v, want its default", e)
	}
}
