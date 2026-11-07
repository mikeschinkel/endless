package gomodcheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/go-dt"
)

// goEnv is the environment every real-go test runs under: the caller's, with
// the network closed so a fixture that would need it fails fast instead of
// hanging, and the toolchain pinned so go.mod can never trigger a download.
func goEnv(t *testing.T) (env []string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOFLAGS=")
	return env
}

// write creates rel under root with body, making directories as needed.
func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", rel, err)
	}
}

// writeDependents writes, under dir, two local modules a and b where a needs b
// at v0.0.1, so a main module requiring both at v0.0.0 understates b.
func writeDependents(t *testing.T, dir string) {
	t.Helper()
	write(t, dir, "a/go.mod", "module example.com/a\n\ngo 1.21\n\nrequire example.com/b v0.0.1\n\nreplace example.com/b => ../b\n")
	write(t, dir, "a/a.go", "package a\n\nimport _ \"example.com/b\"\n")
	write(t, dir, "b/go.mod", "module example.com/b\n\ngo 1.21\n")
	write(t, dir, "b/b.go", "package b\n")
}

// mainModfile is the main module's go.mod requiring a and b, with b at bVersion.
func mainModfile(bVersion string) string {
	return "module example.com/m\n\ngo 1.21\n\nrequire example.com/a v0.0.0\n\nrequire example.com/b " + bVersion + " // indirect\n\n" +
		"replace (\n\texample.com/a => ./a\n\texample.com/b => ./b\n)\n"
}

// An understated transitive requirement — b needed at v0.0.1, listed at
// v0.0.0 — fails with the diff naming the version tidy would write.
func TestRun_UnderstatedRequirementFailsWithDiff(t *testing.T) {
	env := goEnv(t)
	dir := t.TempDir()
	writeDependents(t, dir)
	write(t, dir, "go.mod", mainModfile("v0.0.0"))
	write(t, dir, "m.go", "package m\n\nimport _ \"example.com/a\"\n")

	res, err := Run(dt.DirPath(dir), dt.DirPath(dir), env)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Tidy {
		t.Fatal("Tidy = true, want false for an understated requirement")
	}
	if !strings.Contains(res.Diff, "-require example.com/b v0.0.0") || !strings.Contains(res.Diff, "+require example.com/b v0.0.1") {
		t.Errorf("Diff does not name the version change:\n%s\nstderr:\n%s", res.Diff, res.Detail)
	}
	if want := []string{"./a", "./b"}; strings.Join(res.Rerooted, ",") != strings.Join(want, ",") {
		t.Errorf("Rerooted = %v, want %v", res.Rerooted, want)
	}
}

// The same module with the requirement stated correctly is tidy.
func TestRun_TidyModulePasses(t *testing.T) {
	env := goEnv(t)
	dir := t.TempDir()
	writeDependents(t, dir)
	write(t, dir, "go.mod", mainModfile("v0.0.1"))
	write(t, dir, "m.go", "package m\n\nimport _ \"example.com/a\"\n")

	res, err := Run(dt.DirPath(dir), dt.DirPath(dir), env)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Tidy {
		t.Errorf("Tidy = false, want true; diff:\n%s\nstderr:\n%s", res.Diff, res.Detail)
	}
}

// A relative replace written for the main checkout's depth resolves only from
// there. Re-rooted against the main checkout the worktree's module is tidy;
// against the worktree itself the replacement directory does not exist.
func TestRun_RerootsRelativeReplaceToMainCheckout(t *testing.T) {
	env := goEnv(t)
	parent := t.TempDir()
	main := filepath.Join(parent, "main")
	worktree := filepath.Join(main, ".endless", "worktrees", "e-1")
	modfile := "module example.com/m\n\ngo 1.21\n\nrequire example.com/dep v0.0.0\n\nreplace example.com/dep => ../dep\n"
	write(t, parent, "dep/go.mod", "module example.com/dep\n\ngo 1.21\n")
	write(t, parent, "dep/dep.go", "package dep\n")
	for _, root := range []string{main, worktree} {
		write(t, root, "go.mod", modfile)
		write(t, root, "m.go", "package m\n\nimport _ \"example.com/dep\"\n")
	}

	res, err := Run(dt.DirPath(worktree), dt.DirPath(main), env)
	if err != nil {
		t.Fatalf("Run (re-rooted): %v", err)
	}
	if !res.Tidy {
		t.Errorf("re-rooted: Tidy = false, want true; diff:\n%s\nstderr:\n%s", res.Diff, res.Detail)
	}
	if len(res.Rerooted) != 1 || res.Rerooted[0] != "../dep" {
		t.Errorf("Rerooted = %v, want [../dep]", res.Rerooted)
	}

	res, err = Run(dt.DirPath(worktree), dt.DirPath(worktree), env)
	if err != nil {
		t.Fatalf("Run (unre-rooted): %v", err)
	}
	if res.Tidy || !strings.Contains(res.Detail, "replacement directory") {
		t.Errorf("unre-rooted: Tidy = %v, Detail = %q; want false and a replacement-directory error", res.Tidy, res.Detail)
	}
}

// A replace for a module nothing requires is tolerated even when its path
// resolves nowhere, as Go itself tolerates it.
func TestRun_ToleratesReplaceOfUnrequiredModule(t *testing.T) {
	env := goEnv(t)
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/m\n\ngo 1.21\n\nreplace example.com/ghost => ../../../../nowhere/ghost\n")
	write(t, dir, "m.go", "package m\n")

	res, err := Run(dt.DirPath(dir), dt.DirPath(dir), env)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Tidy {
		t.Errorf("Tidy = false, want true; diff:\n%s\nstderr:\n%s", res.Diff, res.Detail)
	}
}

// Modules finds the root and nested modules and skips the pruned directories.
func TestModules_FindsNestedAndSkipsPruned(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{
		"go.mod", "sub/go.mod", "deep/er/go.mod",
		"vendor/x/go.mod", "testdata/mod/go.mod", ".endless/worktrees/e-9/go.mod", ".git/go.mod",
		"sub/vendor/go.mod",
	} {
		write(t, dir, rel, "module x\n")
	}

	got, err := Modules(dt.DirPath(dir))
	if err != nil {
		t.Fatalf("Modules: %v", err)
	}
	want := []string{".", "deep/er", "sub"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Modules = %v, want %v", got, want)
	}
}

// Modules of a tree with no go.mod is empty, not an error.
func TestModules_NoneForNonGoTree(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "README.md", "hi\n")
	got, err := Modules(dt.DirPath(dir))
	if err != nil || len(got) != 0 {
		t.Errorf("Modules = %v, %v; want none, nil", got, err)
	}
}

// MainCheckout resolves a linked worktree to its main checkout, a main
// checkout to itself, and a directory outside git to itself.
func TestMainCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	parent := t.TempDir()
	main := filepath.Join(parent, "main")
	worktree := filepath.Join(main, ".endless", "worktrees", "e-1")
	write(t, main, "README.md", "hi\n")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", main}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	git("worktree", "add", "-q", "-b", "task", worktree)

	resolved := func(p string) string {
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatalf("EvalSymlinks %s: %v", p, err)
		}
		return r
	}
	for _, tc := range []struct{ name, from, want string }{
		{"linked worktree", worktree, main},
		{"main checkout", main, main},
		{"outside git", parent, parent},
	} {
		got, err := MainCheckout(dt.DirPath(tc.from))
		if err != nil {
			t.Fatalf("%s: MainCheckout: %v", tc.name, err)
		}
		if resolved(string(got)) != resolved(tc.want) {
			t.Errorf("%s: MainCheckout = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// A go older than 1.23 is a failing verdict naming the version found, and tidy
// is never attempted.
func TestRun_OldGoFailsNamingVersion(t *testing.T) {
	fake := t.TempDir()
	write(t, fake, "go", "#!/bin/sh\nif [ \"$1\" = env ]; then echo go1.22.5; exit 0; fi\necho \"fake go must not run $*\" >&2\nexit 7\n")
	if err := os.Chmod(filepath.Join(fake, "go"), 0o755); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/m\n\ngo 1.21\n")

	res, err := Run(dt.DirPath(dir), dt.DirPath(dir), os.Environ())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Tidy || res.OldGo != "go1.22.5" {
		t.Errorf("Result = %+v, want Tidy false and OldGo go1.22.5", res)
	}
}

func TestParseGoVersion(t *testing.T) {
	for _, tc := range []struct {
		in           string
		major, minor int
		ok           bool
	}{
		{"go1.27.3", 1, 27, true},
		{"go1.23rc1", 1, 23, true},
		{"devel go1.28-abcdef Mon Jan 1", 1, 28, true},
		{"", 0, 0, false},
		{"gccgo", 0, 0, false},
	} {
		major, minor, err := parseGoVersion(tc.in)
		if (err == nil) != tc.ok || major != tc.major || minor != tc.minor {
			t.Errorf("parseGoVersion(%q) = %d, %d, %v; want %d, %d, ok=%v", tc.in, major, minor, err, tc.major, tc.minor, tc.ok)
		}
	}
}

// The rewrite touches only relative replace paths, in both forms, and leaves
// every other byte alone.
func TestRerootReplaces(t *testing.T) {
	in := strings.Join([]string{
		"module example.com/m",
		"",
		"go 1.21",
		"",
		"require example.com/a v0.0.0 // ../not-a-path",
		"",
		"replace example.com/a => ../a // local",
		"replace example.com/v v1.0.0 => ./v",
		"replace example.com/q => \"../with space\"",
		"replace example.com/abs => /already/abs",
		"replace example.com/mod => example.com/fork v1.2.3",
		"",
		"replace (",
		"\texample.com/b => ../../b",
		"\texample.com/c => ./c",
		")",
		"",
	}, "\n")
	out, rerooted := rerootReplaces([]byte(in), "/repo/main")
	want := strings.Join([]string{
		"module example.com/m",
		"",
		"go 1.21",
		"",
		"require example.com/a v0.0.0 // ../not-a-path",
		"",
		"replace example.com/a => /repo/a // local",
		"replace example.com/v v1.0.0 => /repo/main/v",
		"replace example.com/q => \"/repo/with space\"",
		"replace example.com/abs => /already/abs",
		"replace example.com/mod => example.com/fork v1.2.3",
		"",
		"replace (",
		"\texample.com/b => /b",
		"\texample.com/c => /repo/main/c",
		")",
		"",
	}, "\n")
	if string(out) != want {
		t.Errorf("rerootReplaces:\n--- got\n%s\n--- want\n%s", out, want)
	}
	if got := strings.Join(rerooted, ","); got != "../a,./v,../with space,../../b,./c" {
		t.Errorf("rerooted = %v", rerooted)
	}
}
