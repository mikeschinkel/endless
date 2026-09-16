package dbcontext_test

import (
	"testing"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/dbcontext"
)

// TestConfigDir_ExplicitWinsOverTheEnvironment proves the precedence the E-1429
// doctrine rests on: a per-invocation directory beats an exported variable. An
// environment that could override the flag would be an environment that can
// silently redirect a migration.
func TestConfigDir_ExplicitWinsOverTheEnvironment(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	t.Setenv("HOME", "/home/someone")

	got := dbcontext.ConfigDir("/explicit")
	if got != "/explicit" {
		t.Errorf("ConfigDir(explicit) = %q, want %q", got, "/explicit")
	}
}

func TestConfigDir_FallsBackToXDGThenHome(t *testing.T) {
	t.Setenv("HOME", "/home/someone")

	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	got := dbcontext.ConfigDir("")
	if got != "/xdg/endless" {
		t.Errorf("ConfigDir(\"\") with XDG set = %q, want %q", got, "/xdg/endless")
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	got = dbcontext.ConfigDir("")
	if got != "/home/someone/.config/endless" {
		t.Errorf("ConfigDir(\"\") with XDG empty = %q, want %q",
			got, "/home/someone/.config/endless")
	}
}

func TestDBPath_IsTheDatabaseInsideTheConfigDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")

	got := dbcontext.DBPath("")
	if got != "/xdg/endless/endless.db" {
		t.Errorf("DBPath(\"\") = %q, want %q", got, "/xdg/endless/endless.db")
	}

	got = dbcontext.DBPath("/explicit")
	if got != "/explicit/endless.db" {
		t.Errorf("DBPath(explicit) = %q, want %q", got, "/explicit/endless.db")
	}
}

// TestConsumeConfigDirFlag covers each shape the flag arrives in, and the two
// that matter most: the flag is stripped from ANYWHERE in the arguments (a
// binary reads args[1] as its subcommand and must not see it), and the
// executable name in args[0] is never touched.
func TestConsumeConfigDirFlag(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantCleaned []string
		wantDir     dt.DirPath
		wantFound   bool
	}{
		{
			name:        "separate value before the subcommand",
			args:        []string{"endless-migrate", "--config-dir", "/cfg", "apply", "x.sql"},
			wantCleaned: []string{"endless-migrate", "apply", "x.sql"},
			wantDir:     "/cfg",
			wantFound:   true,
		},
		{
			name:        "equals form after the subcommand",
			args:        []string{"endless-migrate", "apply", "--config-dir=/cfg", "x.sql"},
			wantCleaned: []string{"endless-migrate", "apply", "x.sql"},
			wantDir:     "/cfg",
			wantFound:   true,
		},
		{
			name:        "absent",
			args:        []string{"endless-migrate", "apply", "x.sql"},
			wantCleaned: []string{"endless-migrate", "apply", "x.sql"},
			wantDir:     "",
			wantFound:   false,
		},
		{
			name:        "last occurrence wins",
			args:        []string{"endless-migrate", "--config-dir", "/one", "--config-dir=/two", "apply"},
			wantCleaned: []string{"endless-migrate", "apply"},
			wantDir:     "/two",
			wantFound:   true,
		},
		{
			name:        "trailing flag names nothing and is dropped",
			args:        []string{"endless-migrate", "apply", "--config-dir"},
			wantCleaned: []string{"endless-migrate", "apply"},
			wantDir:     "",
			wantFound:   false,
		},
		{
			name:        "a value that is an empty string is still a value",
			args:        []string{"endless-migrate", "--config-dir=", "apply"},
			wantCleaned: []string{"endless-migrate", "apply"},
			wantDir:     "",
			wantFound:   true,
		},
		{
			name:        "no arguments at all",
			args:        nil,
			wantCleaned: []string{},
			wantDir:     "",
			wantFound:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned, dir, found := dbcontext.ConsumeConfigDirFlag(tt.args)
			if len(cleaned) != len(tt.wantCleaned) {
				t.Fatalf("cleaned = %q, want %q", cleaned, tt.wantCleaned)
			}
			for i := range cleaned {
				if cleaned[i] != tt.wantCleaned[i] {
					t.Fatalf("cleaned = %q, want %q", cleaned, tt.wantCleaned)
				}
			}
			if dir != tt.wantDir {
				t.Errorf("dir = %q, want %q", dir, tt.wantDir)
			}
			if found != tt.wantFound {
				t.Errorf("found = %v, want %v", found, tt.wantFound)
			}
		})
	}
}

// TestConsumeConfigDirFlag_DoesNotMutateItsInput proves the function is safe to
// call on os.Args: it returns a new slice rather than reordering the caller's.
func TestConsumeConfigDirFlag_DoesNotMutateItsInput(t *testing.T) {
	args := []string{"endless-migrate", "--config-dir", "/cfg", "apply"}
	original := append([]string(nil), args...)

	dbcontext.ConsumeConfigDirFlag(args)

	for i := range args {
		if args[i] != original[i] {
			t.Fatalf("input args mutated: %q, want %q", args, original)
		}
	}
}
