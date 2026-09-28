package migrations

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// stepName matches a migration file in this directory: a numeric version, an
// underscore, a name, and .sql or .go. migrations.go and tests do not match.
var stepName = regexp.MustCompile(`^(\d+)_.+\.(sql|go)$`)

// TestVersions_UniqueContiguousAndConsistent is E-2184's guard for this
// directory. Two worktrees that each add a migration take the same next
// number, and goose refuses the duplicate at provider construction — so the
// failure used to arrive as every command after the land breaking, rather than
// as a test. This moves it to `just test`.
func TestVersions_UniqueContiguousAndConsistent(t *testing.T) {
	registered := make([]int64, 0, len(Go()))
	for _, m := range Go() {
		registered = append(registered, m.Version)
	}
	for _, p := range versionProblems(t, ".", registered) {
		t.Error(p)
	}
}

// TestVersions_GuardCatchesEachFault proves the guard above can fail: each
// fault it exists for, planted in a scratch directory.
func TestVersions_GuardCatchesEachFault(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goStep := func(v int) string {
		return fmt.Sprintf("package migrations\n\nfunc f() { goose.NewGoMigration(%d, nil, nil) }\n", v)
	}
	write("00001_a.sql", "")
	write("00002_b.sql", "")
	write("00002_c.sql", "")       // duplicate version
	write("00003_d.go", goStep(4)) // literal disagrees with filename
	write("00005_e.go", goStep(5)) // gap at 4, and not registered

	got := strings.Join(versionProblems(t, dir, []int64{3, 6}), "\n")
	for _, want := range []string{
		"version 2 is used by 2 files: 00002_b.sql, 00002_c.sql",
		"expected 4, found 5",
		"Go() registers version 6 but no 00006_*.go file exists",
		"00005_e.go is not registered in Go()",
		"00003_d.go calls goose.NewGoMigration(4, …); its filename says 3",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// versionProblems checks one migrations directory against the versions its
// Go() registers, and returns every violation:
//   - every version is used by exactly one file, whatever its language;
//   - versions run 1..N with no gap;
//   - every registered version has a .go file, and every .go step file is
//     registered;
//   - each .go file's NewGoMigration literal equals its filename's version —
//     nothing else stops 00006_x.go from registering 5.
func versionProblems(t *testing.T, dir string, registered []int64) (problems []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	byVersion := map[int64][]string{}
	goFiles := map[int64]string{}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		m := stepName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			add("%s: version %q: %v", e.Name(), m[1], err)
			continue
		}
		byVersion[v] = append(byVersion[v], e.Name())
		if m[2] == "go" {
			goFiles[v] = e.Name()
		}
	}
	if len(byVersion) == 0 {
		t.Fatalf("no migration files in %s; is the test running in its package directory?", dir)
	}

	versions := make([]int64, 0, len(byVersion))
	for v, files := range byVersion {
		versions = append(versions, v)
		if len(files) > 1 {
			sort.Strings(files)
			add("version %d is used by %d files: %s — renumber the newer one "+
				"after the highest version on main", v, len(files), strings.Join(files, ", "))
		}
	}
	slices.Sort(versions)
	for i, v := range versions {
		if want := int64(i + 1); v != want {
			add("versions must run 1..N without a gap: expected %d, found %d (%s)",
				want, v, strings.Join(byVersion[v], ", "))
			break
		}
	}

	isRegistered := map[int64]bool{}
	for _, v := range registered {
		if isRegistered[v] {
			add("Go() registers version %d more than once", v)
		}
		isRegistered[v] = true
		if _, ok := goFiles[v]; !ok {
			add("Go() registers version %d but no %05d_*.go file exists", v, v)
		}
	}
	goVersions := make([]int64, 0, len(goFiles))
	for v := range goFiles {
		goVersions = append(goVersions, v)
	}
	slices.Sort(goVersions)
	for _, v := range goVersions {
		name := goFiles[v]
		if !isRegistered[v] {
			add("%s is not registered in Go()", name)
		}
		lit, err := newGoMigrationVersion(filepath.Join(dir, name))
		if err != nil {
			add("%s: %v", name, err)
			continue
		}
		if lit != v {
			add("%s calls goose.NewGoMigration(%d, …); its filename says %d", name, lit, v)
		}
	}
	return problems
}

// newGoMigrationVersion returns the integer literal a file passes as the first
// argument of goose.NewGoMigration. Exactly one call, with a literal, is
// required: a computed version is one this test cannot check against the name.
func newGoMigrationVersion(file string) (int64, error) {
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		return 0, err
	}
	var found []ast.Expr
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "NewGoMigration" && len(call.Args) > 0 {
			found = append(found, call.Args[0])
		}
		return true
	})
	if len(found) != 1 {
		return 0, fmt.Errorf("want exactly one goose.NewGoMigration call, found %d", len(found))
	}
	lit, ok := found[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return 0, fmt.Errorf("goose.NewGoMigration's version must be an integer literal")
	}
	return strconv.ParseInt(lit.Value, 0, 64)
}
