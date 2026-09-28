package landgate

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/go-cfgstore"
)

// cfgstore panics without a logger; config.Load reaches it. Same pattern as
// internal/projectstatuscmd/sessionname_test.go.
func init() {
	cfgstore.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// fixture is a main checkout plus a task worktree cut from it, in one temp
// repo, the shape `endless task claim` produces.
type fixture struct {
	t    *testing.T
	main string
	wt   string
}

func newFixture(t *testing.T, config string) *fixture {
	t.Helper()
	// config.Load reads (and may create) the CLI layer under the user's config
	// dir; keep it out of the real one.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	root := t.TempDir()
	f := &fixture{t: t, main: filepath.Join(root, "main"), wt: filepath.Join(root, "wt")}
	f.git(root, "init", "-q", "-b", "main", f.main)
	f.git(f.main, "config", "user.email", "t@example.com")
	f.git(f.main, "config", "user.name", "t")
	f.write(f.main, ".endless/config.json", config)
	f.write(f.main, "db/migrations/00001_base.sql", "-- base\n")
	f.write(f.main, "db/migrations/00002_more.sql", "-- more\n")
	f.commit(f.main, "base")
	f.git(f.main, "worktree", "add", "-q", "-b", "task/1", f.wt)
	return f
}

const withDirs = `{"name":"p","migrations":{"dirs":["db/migrations"]}}`

func (f *fixture) git(dir string, args ...string) string {
	f.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (f *fixture) write(dir, rel, body string) {
	f.t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) commit(dir, msg string) {
	f.t.Helper()
	f.git(dir, "add", "-A")
	f.git(dir, "commit", "-q", "-m", msg)
}

func (f *fixture) check() Verdict {
	f.t.Helper()
	v, err := Check(Args{ProjectRoot: f.main, Worktree: f.wt, Base: "main", Task: "E-1"})
	if err != nil {
		f.t.Fatalf("Check: %v", err)
	}
	return v
}

func TestCheck_RefusesWhenBothSidesAddMigrations(t *testing.T) {
	f := newFixture(t, withDirs)
	f.write(f.wt, "db/migrations/00003_branch.sql", "-- branch\n")
	f.commit(f.wt, "branch migration")
	f.write(f.main, "db/migrations/00003_main.sql", "-- main\n")
	f.commit(f.main, "main migration")

	v := f.check()
	if !v.Refused || v.Source != SourceMigrations {
		t.Fatalf("want a migrations refusal, got %+v", v)
	}
	if got := strings.Join(v.Landed, ","); got != "db/migrations/00003_main.sql" {
		t.Errorf("Landed = %q", got)
	}
	if len(v.Branch) != 1 || v.Branch[0].To != "db/migrations/00004_branch.sql" {
		t.Errorf("Branch = %+v, want 00003_branch.sql → 00004_branch.sql", v.Branch)
	}
	if strings.Contains(v.Summary, "\n") {
		t.Errorf("Summary must be one line: %q", v.Summary)
	}
	for _, want := range []string{
		"db/migrations/00003_main.sql",
		"db/migrations/00003_branch.sql  →  db/migrations/00004_branch.sql",
		"git rebase main",
		"endless sandbox reset",
		"endless task verify E-1",
		"endless worktree land E-1",
	} {
		if !strings.Contains(v.Block, want) {
			t.Errorf("Block missing %q:\n%s", want, v.Block)
		}
	}
}

func TestCheck_AllowsWhenOnlyOneSideAdds(t *testing.T) {
	t.Run("branch only", func(t *testing.T) {
		f := newFixture(t, withDirs)
		f.write(f.wt, "db/migrations/00003_branch.sql", "x\n")
		f.commit(f.wt, "branch")
		f.write(f.main, "other.txt", "x\n")
		f.commit(f.main, "unrelated")
		if v := f.check(); v.Refused {
			t.Fatalf("refused: %+v", v)
		}
	})
	t.Run("main only", func(t *testing.T) {
		f := newFixture(t, withDirs)
		f.write(f.wt, "code.go", "package x\n")
		f.commit(f.wt, "branch")
		f.write(f.main, "db/migrations/00003_main.sql", "x\n")
		f.commit(f.main, "main")
		if v := f.check(); v.Refused {
			t.Fatalf("refused: %+v", v)
		}
	})
}

// After the branch has rebased past what landed, the merge-base holds main's
// migration and the gate has nothing to say — which is how the refusal's own
// instructions clear it.
func TestCheck_AllowsOnceRebasedAndRenumbered(t *testing.T) {
	f := newFixture(t, withDirs)
	f.write(f.wt, "db/migrations/00003_branch.sql", "x\n")
	f.commit(f.wt, "branch")
	f.write(f.main, "db/migrations/00003_main.sql", "x\n")
	f.commit(f.main, "main")
	if !f.check().Refused {
		t.Fatal("precondition: expected a refusal before the rebase")
	}
	f.git(f.wt, "rebase", "-q", "main")
	f.git(f.wt, "mv", "db/migrations/00003_branch.sql", "db/migrations/00004_branch.sql")
	f.commit(f.wt, "renumber")
	if v := f.check(); v.Refused {
		t.Fatalf("still refused after rebase + renumber: %+v", v)
	}
}

func TestCheck_NoConfiguredDirsNoCheck(t *testing.T) {
	f := newFixture(t, `{"name":"p"}`)
	f.write(f.wt, "db/migrations/00003_branch.sql", "x\n")
	f.commit(f.wt, "branch")
	f.write(f.main, "db/migrations/00003_main.sql", "x\n")
	f.commit(f.main, "main")
	if v := f.check(); v.Refused {
		t.Fatalf("refused without a migrations declaration: %+v", v)
	}
}

func TestCheck_NonNumericNamesGetNoRenameProposal(t *testing.T) {
	f := newFixture(t, withDirs)
	f.write(f.wt, "db/migrations/abc123_branch.py", "x\n")
	f.commit(f.wt, "branch")
	f.write(f.main, "db/migrations/def456_main.py", "x\n")
	f.commit(f.main, "main")
	v := f.check()
	if !v.Refused || v.Branch[0].To != "" {
		t.Fatalf("want refusal with no proposal, got %+v", v)
	}
	if !strings.Contains(v.Block, "whatever terms your migration tool uses") {
		t.Errorf("Block should fall back to tool-neutral wording:\n%s", v.Block)
	}
}

func TestRenames_PreservesWidthAndOrder(t *testing.T) {
	got := renames(
		[]string{"m/00003_a.sql", "m/00004_b.go"},
		[]string{"m/00001_x.sql", "m/00005_y.sql", "m/migrations.go"},
	)
	if got[0].To != "m/00006_a.sql" || got[1].To != "m/00007_b.go" {
		t.Fatalf("renames = %+v", got)
	}
}

func writeHook(t *testing.T, f *fixture, body string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(f.main, HookPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func TestCheck_HookAllows(t *testing.T) {
	f := newFixture(t, `{"name":"p"}`)
	writeHook(t, f, "#!/bin/sh\nexit 0\n", 0o755)
	if v := f.check(); v.Refused {
		t.Fatalf("refused: %+v", v)
	}
}

func TestCheck_HookVetoesWithItsOwnExplanation(t *testing.T) {
	f := newFixture(t, `{"name":"p"}`)
	writeHook(t, f, `#!/bin/sh
echo "cannot land $ENDLESS_TASK_ID onto $2: schema freeze"
echo "Wait for the freeze to lift, then land again."
exit 3
`, 0o755)
	v := f.check()
	if !v.Refused || v.Source != SourceHook {
		t.Fatalf("want a hook refusal, got %+v", v)
	}
	if v.Summary != "cannot land E-1 onto main: schema freeze" {
		t.Errorf("Summary = %q", v.Summary)
	}
	if strings.TrimSpace(v.Block) != "Wait for the freeze to lift, then land again." {
		t.Errorf("Block = %q", v.Block)
	}
}

func TestCheck_SilentHookStillRefuses(t *testing.T) {
	f := newFixture(t, `{"name":"p"}`)
	writeHook(t, f, "#!/bin/sh\necho oops >&2\nexit 1\n", 0o755)
	v := f.check()
	if !v.Refused || !strings.Contains(v.Summary, "exit 1") || !strings.Contains(v.Block, "oops") {
		t.Fatalf("got %+v", v)
	}
}

func TestCheck_NonExecutableHookRefuses(t *testing.T) {
	f := newFixture(t, `{"name":"p"}`)
	writeHook(t, f, "#!/bin/sh\nexit 0\n", 0o644)
	v := f.check()
	if !v.Refused || !strings.Contains(v.Block, "chmod +x") {
		t.Fatalf("got %+v", v)
	}
}

// The built-in check runs first, and a refusal there does not consult the hook.
func TestCheck_MigrationRefusalPreemptsHook(t *testing.T) {
	f := newFixture(t, withDirs)
	f.write(f.wt, "db/migrations/00003_branch.sql", "x\n")
	f.commit(f.wt, "branch")
	f.write(f.main, "db/migrations/00003_main.sql", "x\n")
	f.commit(f.main, "main")
	writeHook(t, f, "#!/bin/sh\necho hook ran\nexit 1\n", 0o755)
	if v := f.check(); v.Source != SourceMigrations {
		t.Fatalf("want the migrations refusal, got %+v", v)
	}
}
