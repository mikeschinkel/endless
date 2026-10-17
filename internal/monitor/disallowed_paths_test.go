package monitor

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withTestHome points $HOME at a fresh resolved temp directory, so rule 1 (and
// every ~ entry) applies to a tree the test owns.
func withTestHome(t *testing.T) string {
	t.Helper()
	home := mkdirs(t, tempProjectRoot(t), "home", "tester")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	return home
}

// writeOverrides writes the user's overrides file into the test's config dir.
func writeOverrides(t *testing.T, body string) {
	t.Helper()
	p := filepath.Join(ConfigDir(), DisallowedOverridesFile)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedStored seeds a project row in STORED form, the way register writes it.
func seedStored(t *testing.T, db *sql.DB, id int64, name, dir string) {
	t.Helper()
	stored, err := StoredProjectPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedProject(t, db, id, name, stored)
}

func resolve(t *testing.T, db *sql.DB, dir string) DirVerdict {
	t.Helper()
	v, err := ResolveDirectory(db, dir)
	if err != nil {
		t.Fatalf("ResolveDirectory(%s): %v", dir, err)
	}
	return v
}

func wantKind(t *testing.T, v DirVerdict, kind string) {
	t.Helper()
	if v.Kind != kind {
		t.Fatalf("%s: kind = %q (%+v), want %q", v.Path, v.Kind, v, kind)
	}
}

func TestResolveDirectory_HomeAndAboveAreDisallowed(t *testing.T) {
	db := withTestDB(t)
	home := withTestHome(t)

	v := resolve(t, db, home)
	wantKind(t, v, VerdictDisallowed)
	if v.Scope != ScopeSelf {
		t.Errorf("home scope = %q, want self", v.Scope)
	}
	wantKind(t, resolve(t, db, filepath.Dir(home)), VerdictDisallowed)
}

// A directory outside $HOME stays allowed: /work/app in a container or CI.
func TestResolveDirectory_OutsideHomeIsAllowed(t *testing.T) {
	db := withTestDB(t)
	withTestHome(t)
	work := mkdirs(t, tempProjectRoot(t), "work", "app")
	wantKind(t, resolve(t, db, work), VerdictNone)
}

// The bug that filed E-2369: a project row at $HOME made every directory under
// it resolve to that project.
func TestResolveDirectory_HomeRowIsNeverInherited(t *testing.T) {
	db := withTestDB(t)
	home := withTestHome(t)
	seedStored(t, db, 1, "tester", home)
	repo := mkdirs(t, home, "code", "repo")

	wantKind(t, resolve(t, db, repo), VerdictNone)
	wantKind(t, resolve(t, db, home), VerdictDisallowed)
}

func TestProjectIDForPath_NeverAutoRegistersHome(t *testing.T) {
	db := withTestDB(t)
	home := withTestHome(t)
	before := countProjects(t, db)

	_, _, err := ProjectIDForPath(home)
	if !errors.Is(err, ErrDisallowedDirectory) || !NotAProject(err) {
		t.Fatalf("err = %v, want ErrDisallowedDirectory", err)
	}
	if got := countProjects(t, db); got != before {
		t.Fatalf("projects = %d, want %d: home was auto-registered", got, before)
	}
}

func TestResolveDirectory_SelfAndSubtreeOverrides(t *testing.T) {
	db := withTestDB(t)
	home := withTestHome(t)
	writeOverrides(t, `{"add": [
		{"path": "~/Docs", "scope": "self"},
		{"path": "~/Dl", "scope": "subtree"}
	]}`)
	docs := mkdirs(t, home, "Docs")
	dl := mkdirs(t, home, "Dl")
	inDl := mkdirs(t, dl, "repo", "src")
	// A row nearer than the subtree root does not rescue a directory under it.
	seedStored(t, db, 1, "repo", filepath.Dir(inDl))

	wantKind(t, resolve(t, db, docs), VerdictDisallowed)
	wantKind(t, resolve(t, db, mkdirs(t, docs, "foo")), VerdictNone)
	v := resolve(t, db, inDl)
	wantKind(t, v, VerdictDisallowed)
	if v.Scope != ScopeSubtree {
		t.Errorf("scope = %q, want subtree", v.Scope)
	}
}

// Removing a built-in entry is how someone who keeps /etc in git opts back in.
func TestResolveDirectory_OverrideRemovesABuiltin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/etc is a unix entry")
	}
	db := withTestDB(t)
	withTestHome(t)
	etc := resolveAbs("/etc")

	wantKind(t, resolve(t, db, etc), VerdictDisallowed)
	writeOverrides(t, `{"remove": ["/etc"]}`)
	wantKind(t, resolve(t, db, etc), VerdictNone)
}

func TestResolveDirectory_HomeCannotBeRemoved(t *testing.T) {
	db := withTestDB(t)
	home := withTestHome(t)
	writeOverrides(t, `{"remove": ["~"]}`)
	wantKind(t, resolve(t, db, home), VerdictDisallowed)
}

func TestResolveDirectory_ParentDirStopsTheWalk(t *testing.T) {
	for _, via := range []string{"override", "marker"} {
		t.Run(via, func(t *testing.T) {
			db := withTestDB(t)
			home := withTestHome(t)
			code := mkdirs(t, home, "Code")
			seedStored(t, db, 1, "code", code)
			parent := mkdirs(t, code, "Repos")
			if via == "override" {
				writeOverrides(t, `{"add": [{"path": "~/Code/Repos", "parent_dir": true}]}`)
			} else {
				if err := os.WriteFile(filepath.Join(parent, ParentDirMarker), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			wantKind(t, resolve(t, db, parent), VerdictDisallowed)
			// Without the parent directory this would be ~/Code's project.
			wantKind(t, resolve(t, db, mkdirs(t, parent, "app")), VerdictNone)
		})
	}
}

func TestResolveDirectory_ProjectsIsABuiltinParentDir(t *testing.T) {
	db := withTestDB(t)
	home := withTestHome(t)
	projects := mkdirs(t, home, "Projects")
	v := resolve(t, db, projects)
	wantKind(t, v, VerdictDisallowed)
	if !strings.Contains(v.Rule, "parent directory") {
		t.Errorf("rule = %q, want a parent-directory rule", v.Rule)
	}
	wantKind(t, resolve(t, db, mkdirs(t, projects, "app")), VerdictNone)
}

func TestResolveDirectory_IgnoreMarkerStillWins(t *testing.T) {
	db := withTestDB(t)
	home := withTestHome(t)
	dir := mkdirs(t, home, "Projects", "vendor")
	if err := os.WriteFile(filepath.Join(dir, IgnoreMarker), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	wantKind(t, resolve(t, db, mkdirs(t, dir, "x")), VerdictIgnored)
}

func TestResolveDirectory_NestedProjects(t *testing.T) {
	db := withTestDB(t)
	home := withTestHome(t)
	app := mkdirs(t, home, "work", "app")
	seedStored(t, db, 1, "app", app)

	// Inside a project: the ancestor's row, marked Within.
	v := resolve(t, db, mkdirs(t, app, "sub"))
	wantKind(t, v, VerdictProject)
	if !v.Within {
		t.Error("a directory inside a project should be Within")
	}
	// The project's own directory is not Within.
	if v := resolve(t, db, app); v.Within {
		t.Error("a project's own directory should not be Within")
	}

	// Above a project: none, but Encloses names it.
	work := filepath.Dir(app)
	v = resolve(t, db, work)
	wantKind(t, v, VerdictNone)
	if v.Encloses != "~/work/app" {
		t.Errorf("encloses = %q, want ~/work/app", v.Encloses)
	}

	// ...and the hook will not auto-register it.
	before := countProjects(t, db)
	if _, _, err := ProjectIDForPath(work); !errors.Is(err, ErrDisallowedDirectory) {
		t.Fatalf("err = %v, want ErrDisallowedDirectory", err)
	}
	if got := countProjects(t, db); got != before {
		t.Fatalf("projects = %d, want %d", got, before)
	}
}

// An ignored row is not a project, so it is not one a directory encloses.
func TestResolveDirectory_IgnoredRowsDoNotNest(t *testing.T) {
	db := withTestDB(t)
	home := withTestHome(t)
	vendor := mkdirs(t, home, "work", "vendor")
	seedIgnored(t, vendor)
	if v := resolve(t, db, filepath.Dir(vendor)); v.Encloses != "" {
		t.Errorf("encloses = %q, want none", v.Encloses)
	}
}

func TestResolveDirectory_DownloadsIsASubtree(t *testing.T) {
	db := withTestDB(t)
	home := withTestHome(t)
	switch runtime.GOOS {
	case "darwin":
	case "linux":
		cfg := mkdirs(t, home, ".config")
		if err := os.WriteFile(filepath.Join(cfg, "user-dirs.dirs"),
			[]byte(`XDG_DOWNLOAD_DIR="$HOME/Downloads"`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	default:
		t.Skip("known folders come from the OS")
	}
	v := resolve(t, db, mkdirs(t, home, "Downloads", "some-repo-main"))
	wantKind(t, v, VerdictDisallowed)
	if v.Scope != ScopeSubtree {
		t.Errorf("scope = %q, want subtree", v.Scope)
	}
}

func TestReadOverrides_MalformedFileNamesIt(t *testing.T) {
	db := withTestDB(t)
	home := withTestHome(t)
	writeOverrides(t, `{"add": [`)
	_, err := ResolveDirectory(db, home)
	if err == nil || !strings.Contains(err.Error(), DisallowedOverridesFile) {
		t.Fatalf("err = %v, want one naming %s", err, DisallowedOverridesFile)
	}
}

func TestReadOverrides_WritesASkeletonOnce(t *testing.T) {
	db := withTestDB(t)
	home := withTestHome(t)
	resolve(t, db, home)
	data, err := os.ReadFile(filepath.Join(ConfigDir(), DisallowedOverridesFile))
	if err != nil {
		t.Fatalf("skeleton not written: %v", err)
	}
	if !strings.Contains(string(data), `"add": []`) || !strings.Contains(string(data), `"remove": []`) {
		t.Errorf("skeleton = %s", data)
	}
}

func TestXDGUserDir_LocalizedAndDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	cfg := filepath.Join(home, ".config")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# written by xdg-user-dirs-update\n" +
		`XDG_DOCUMENTS_DIR="$HOME/Dokumente"` + "\n" +
		`XDG_TEMPLATES_DIR="$HOME/"` + "\n"
	if err := os.WriteFile(filepath.Join(cfg, "user-dirs.dirs"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, ok := xdgUserDir("Documents", home); !ok || p != filepath.Join(home, "Dokumente") {
		t.Errorf("Documents = %q, %v; want the localized ~/Dokumente", p, ok)
	}
	if _, ok := xdgUserDir("Templates", home); ok {
		t.Error("a folder set to $HOME/ is disabled and should be absent")
	}
	if _, ok := xdgUserDir("Music", home); ok {
		t.Error("a folder missing from the file should be absent")
	}
}

func TestKnownFolder_MacOSCallsVideosMovies(t *testing.T) {
	if p, _ := knownFolder("darwin", "Videos", "/h"); p != filepath.Join("/h", "Movies") {
		t.Errorf("Videos on macOS = %q, want /h/Movies", p)
	}
}

func TestExpandWindowsVars(t *testing.T) {
	t.Setenv("ENDLESS_TEST_ROOT", `C:\Windows`)
	if p, ok := expandWindowsVars(`%ENDLESS_TEST_ROOT%\System32`); !ok || p != `C:\Windows\System32` {
		t.Errorf("got %q, %v", p, ok)
	}
	if _, ok := expandWindowsVars(`%ENDLESS_TEST_UNSET_VAR%`); ok {
		t.Error("an unset variable should skip the entry")
	}
}

// The guide page is generated from the list; a list edit without `just
// disallowed-doc` fails here rather than shipping a page that lies.
func TestDisallowedPathsDocIsCurrent(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "guide", "disallowed-paths.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != DisallowedPathsMarkdown() {
		t.Fatal("docs/guide/disallowed-paths.md is stale: run `just disallowed-doc`")
	}
}
