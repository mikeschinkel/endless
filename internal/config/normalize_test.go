package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/settings"
)

// writeProject writes body as a project's .endless/config.json under a temp
// HOME and returns the project directory and the file.
func writeProject(t *testing.T, body string) (dir dt.DirPath, file string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	proj := t.TempDir()
	file = filepath.Join(proj, ".endless", "config.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dt.DirPath(proj), file
}

// TestNormalize_AcceptsTheThreeSpellings: "on", "off" and "unset" load, and
// keep their text — the file layer does not interpret them.
func TestNormalize_AcceptsTheThreeSpellings(t *testing.T) {
	dir, _ := writeProject(t, `{"main_sync":{"enabled":"on"},"prime":{"enabled":"off"},
		"auto_spawn":{"enabled":"unset"},"checks":{"session_audit":"on"}}`)
	cfg, err := LoadProject(dir)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if cfg.MainSync.Enabled != "on" || cfg.Prime.Enabled != "off" ||
		cfg.AutoSpawn.Enabled != "unset" || cfg.Checks["session_audit"] != "on" {
		t.Errorf("loaded %+v", cfg)
	}
}

// TestNormalize_RefusesALegacyBoolean: a JSON true is refused with the file,
// the key and the text to write instead — not encoding/json's "cannot
// unmarshal bool", which names none of them.
func TestNormalize_RefusesALegacyBoolean(t *testing.T) {
	dir, file := writeProject(t, `{"main_sync":{"enabled":true},"checks":{"bash_git_writes":false}}`)
	_, err := LoadProject(dir)
	if !errors.Is(err, settings.ErrLegacyBool) || !errors.Is(err, ErrInvalidOptIn) {
		t.Fatalf("LoadProject = %v, want ErrInvalidOptIn wrapping ErrLegacyBool", err)
	}
	msg := err.Error()
	for _, want := range []string{file, "main_sync.enabled", "checks.bash_git_writes", `write "on"`, `write "off"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not mention %q:\n%s", want, msg)
		}
	}
}

func TestNormalize_RefusesAnUnknownSpelling(t *testing.T) {
	dir, _ := writeProject(t, `{"prime":{"enabled":"yes"}}`)
	_, err := LoadProject(dir)
	if !errors.Is(err, settings.ErrUnknownOptIn) {
		t.Fatalf("LoadProject = %v, want ErrUnknownOptIn", err)
	}
	if !strings.Contains(err.Error(), "prime.enabled") {
		t.Errorf("error does not name the key: %v", err)
	}
}

// TestOptIns lists only the opt-ins a config sets, one per checks key.
func TestOptIns(t *testing.T) {
	cfg := &EndlessConfig{
		MainSync: MainSync{Enabled: "on"},
		Checks:   map[string]OptInText{"b": "off", "a": "on"},
	}
	var got []string
	for _, kv := range cfg.OptIns() {
		got = append(got, kv.Key+"="+kv.Value.String())
	}
	want := "checks.a=on checks.b=off main_sync.enabled=on"
	if strings.Join(got, " ") != want {
		t.Errorf("OptIns() = %v, want %s", got, want)
	}
}

// TestLeafKeys walks into objects and stops at maps and pointers.
func TestLeafKeys(t *testing.T) {
	keys := strings.Join(LeafKeys(), " ")
	for _, want := range []string{"checks", "main_sync.enabled", "auto_spawn.placement",
		"project_status.colors.urgent.bg", "documents.rules", "roots"} {
		if !strings.Contains(" "+keys+" ", " "+want+" ") {
			t.Errorf("LeafKeys() lacks %s: %s", want, keys)
		}
	}
	for _, not := range []string{"main_sync ", "project_status ", "checks."} {
		if strings.Contains(keys+" ", not) {
			t.Errorf("LeafKeys() has non-leaf %q: %s", not, keys)
		}
	}
}
