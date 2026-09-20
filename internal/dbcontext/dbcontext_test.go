package dbcontext_test

import (
	"errors"
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

// TestMainConfigDir_IgnoresXDG is the assertion that makes `--db main` mean
// main. Endless injects XDG_CONFIG_HOME to route a child process at a
// worktree's sandbox, so a resolver that honoured it would answer "the sandbox"
// to a caller that said "main" — the wrong-database failure the flag exists to
// prevent, inside the flag that exists to prevent it.
func TestMainConfigDir_IgnoresXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/a/worktree/.endless/sandbox")
	t.Setenv("HOME", "/home/someone")

	got, err := dbcontext.MainConfigDir()
	if err != nil {
		t.Fatalf("MainConfigDir() error = %v", err)
	}
	if got != "/home/someone/.config/endless" {
		t.Errorf("MainConfigDir() = %q, want %q", got, "/home/someone/.config/endless")
	}
}

// TestMainConfigDir_FollowsHOME is the other half of the same contract, and the
// one a verify suite depends on: it runs under a temp HOME and says `--db main`
// meaning its own isolated main, not the developer's real one.
func TestMainConfigDir_FollowsHOME(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/tmp/verify-run-1234")

	got, err := dbcontext.MainConfigDir()
	if err != nil {
		t.Fatalf("MainConfigDir() error = %v", err)
	}
	if got != "/tmp/verify-run-1234/.config/endless" {
		t.Errorf("MainConfigDir() = %q, want %q", got, "/tmp/verify-run-1234/.config/endless")
	}
}

// TestMainConfigDir_IsNotConfigDir pins the distinction that E-2157 had to
// resist collapsing. With XDG set the two resolvers MUST disagree; a refactor
// that makes them agree has silently redirected every `--db main`.
func TestMainConfigDir_IsNotConfigDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	t.Setenv("HOME", "/home/someone")

	main, err := dbcontext.MainConfigDir()
	if err != nil {
		t.Fatalf("MainConfigDir() error = %v", err)
	}
	if def := dbcontext.ConfigDir(""); def == main {
		t.Fatalf("ConfigDir(\"\") and MainConfigDir() both = %q; --db main must "+
			"ignore XDG_CONFIG_HOME while the default honours it", main)
	}
}

func TestMainDBPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	t.Setenv("HOME", "/home/someone")

	got, err := dbcontext.MainDBPath()
	if err != nil {
		t.Fatalf("MainDBPath() error = %v", err)
	}
	if got != "/home/someone/.config/endless/endless.db" {
		t.Errorf("MainDBPath() = %q, want %q", got,
			"/home/someone/.config/endless/endless.db")
	}
}

// TestConsumeFlags covers each shape a flag arrives in, and the two properties
// that matter most: a flag is stripped from ANYWHERE in the arguments (a binary
// reads args[1] as its subcommand and must not see one), and args[0] is never
// touched.
func TestConsumeFlags(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantCleaned []string
		wantChoice  dbcontext.Choice
		wantDir     dt.DirPath
	}{
		{
			name:        "--db main before the subcommand",
			args:        []string{"endless-migrate", "--db", "main", "apply", "x.sql"},
			wantCleaned: []string{"endless-migrate", "apply", "x.sql"},
			wantChoice:  dbcontext.ChoiceMain,
		},
		{
			name:        "--db=main equals form after the subcommand",
			args:        []string{"endless-go", "event", "--db=main", "emit"},
			wantCleaned: []string{"endless-go", "event", "emit"},
			wantChoice:  dbcontext.ChoiceMain,
		},
		{
			name:        "--db sandbox parses to a choice, unresolved",
			args:        []string{"endless-go", "--db", "sandbox", "session-query"},
			wantCleaned: []string{"endless-go", "session-query"},
			wantChoice:  dbcontext.ChoiceSandbox,
		},
		{
			name:        "--db-dir separate value",
			args:        []string{"endless-migrate", "--db-dir", "/cfg", "apply", "x.sql"},
			wantCleaned: []string{"endless-migrate", "apply", "x.sql"},
			wantChoice:  dbcontext.ChoiceDir,
			wantDir:     "/cfg",
		},
		{
			name:        "--db-dir equals form after the subcommand",
			args:        []string{"endless-migrate", "apply", "--db-dir=/cfg", "x.sql"},
			wantCleaned: []string{"endless-migrate", "apply", "x.sql"},
			wantChoice:  dbcontext.ChoiceDir,
			wantDir:     "/cfg",
		},
		{
			name:        "absent",
			args:        []string{"endless-migrate", "apply", "x.sql"},
			wantCleaned: []string{"endless-migrate", "apply", "x.sql"},
			wantChoice:  dbcontext.ChoiceNone,
		},
		{
			name:        "a --db-dir value that is an empty string is still a value",
			args:        []string{"endless-migrate", "--db-dir=", "apply"},
			wantCleaned: []string{"endless-migrate", "apply"},
			wantChoice:  dbcontext.ChoiceDir,
			wantDir:     "",
		},
		{
			name:        "no arguments at all",
			args:        nil,
			wantCleaned: []string{},
			wantChoice:  dbcontext.ChoiceNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned, flags, err := dbcontext.ConsumeFlags(tt.args)
			if err != nil {
				t.Fatalf("ConsumeFlags() error = %v, want nil", err)
			}
			if len(cleaned) != len(tt.wantCleaned) {
				t.Fatalf("cleaned = %q, want %q", cleaned, tt.wantCleaned)
			}
			for i := range cleaned {
				if cleaned[i] != tt.wantCleaned[i] {
					t.Fatalf("cleaned = %q, want %q", cleaned, tt.wantCleaned)
				}
			}
			if flags.Choice != tt.wantChoice {
				t.Errorf("Choice = %v, want %v", flags.Choice, tt.wantChoice)
			}
			if flags.Dir != tt.wantDir {
				t.Errorf("Dir = %q, want %q", flags.Dir, tt.wantDir)
			}
		})
	}
}

// TestConsumeFlags_Refusals covers what the merged parser made STRICTER.
//
// internal/dbcontext's own parser used to drop a trailing bare flag silently
// and let a repeated flag win last-one-wins, also silently. Both are a caller
// that meant to choose and did not, and E-1668 had already decided for
// endless-go that such a caller is told. Merging the two parsers (E-2157) is
// what extends that answer to cmd/endless-migrate.
func TestConsumeFlags_Refusals(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want error
	}{
		{
			name: "trailing bare --db names nothing",
			args: []string{"endless-migrate", "apply", "--db"},
			want: dbcontext.ErrDBFlagNeedsValue,
		},
		{
			name: "trailing bare --db-dir names nothing",
			args: []string{"endless-migrate", "apply", "--db-dir"},
			want: dbcontext.ErrDBDirFlagNeedsDir,
		},
		{
			name: "--db and --db-dir are one choice spelled twice",
			args: []string{"endless-go", "--db", "main", "--db-dir", "/tmp/x", "event"},
			want: dbcontext.ErrDBFlagConflict,
		},
		{
			name: "a word outside the vocabulary",
			args: []string{"endless-go", "--db", "worktree", "event"},
			want: dbcontext.ErrUnknownDBValue,
		},
		{
			name: "the retired --config-dir names its replacement",
			args: []string{"endless-migrate", "--config-dir", "/cfg", "apply"},
			want: dbcontext.ErrConfigDirFlagRetired,
		},
		{
			name: "the retired --config-dir in equals form too",
			args: []string{"endless-migrate", "--config-dir=/cfg", "apply"},
			want: dbcontext.ErrConfigDirFlagRetired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := dbcontext.ConsumeFlags(tt.args)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ConsumeFlags() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestConsumeFlags_DoesNotMutateItsInput proves the parser is safe to call on
// os.Args: it returns a new slice rather than reordering the caller's.
func TestConsumeFlags_DoesNotMutateItsInput(t *testing.T) {
	args := []string{"endless-migrate", "--db-dir", "/cfg", "apply"}
	original := append([]string(nil), args...)

	dbcontext.ConsumeFlags(args)

	for i := range args {
		if args[i] != original[i] {
			t.Fatalf("input args mutated: %q, want %q", args, original)
		}
	}
}
