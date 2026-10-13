package tmuxtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// scanRoots are the trees whose tests can start a tmux server, relative to the
// repository root two levels up from this package.
var scanRoots = []string{"internal", "cmd"}

// socketFlags are the tmux flags that name a server's socket. Either one,
// without TMUX_TMPDIR pointed somewhere private, puts the socket in the shared
// system directory (-L) or wherever the test chose (-S).
var socketFlags = map[string]bool{`"-L"`: true, `"-S"`: true}

const rule = `

A test that starts tmux with -L or -S must call tmuxtest.Private(t) first, so
its server's socket lives in a private directory that is removed when the test
ends. Without it the socket lands in the shared system tmux directory, which
Endless's monitor scans on every tick, and stays there (E-2310).`

// unguarded reports the position of the first tmux socket flag in a file that
// mentions tmux and never calls tmuxtest.Private, or token.NoPos.
//
// It works at the file level rather than following each exec.Command: the
// flag is usually built into an argv slice a line or two away from the call,
// and a test file that starts a scratch server is one that should go private.
func unguarded(file *ast.File) token.Pos {
	var tmux, private bool
	flag := token.NoPos
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			switch {
			case n.Kind != token.STRING:
			case n.Value == `"tmux"`:
				tmux = true
			case socketFlags[n.Value] && flag == token.NoPos:
				flag = n.Pos()
			}
		case *ast.SelectorExpr:
			if pkg, ok := n.X.(*ast.Ident); ok && pkg.Name == "tmuxtest" && n.Sel.Name == "Private" {
				private = true
			}
		}
		return true
	})
	if !tmux || private {
		return token.NoPos
	}
	return flag
}

// TestTmuxTestsUsePrivate fails on any _test.go under internal/ or cmd/ that
// starts tmux with -L or -S without calling tmuxtest.Private.
func TestTmuxTestsUsePrivate(t *testing.T) {
	root := repoRoot(t)
	self := filepath.Join(root, "internal", "tmuxtest")
	fset := token.NewFileSet()
	var offenders []string
	scanned := 0

	for _, dir := range scanRoots {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.IsDir():
				if path == self || d.Name() == "testdata" ||
					(strings.HasPrefix(d.Name(), ".") && d.Name() != ".") {
					return fs.SkipDir
				}
				return nil
			case !strings.HasSuffix(path, "_test.go"):
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			scanned++
			if pos := unguarded(file); pos != token.NoPos {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = path
				}
				offenders = append(offenders, fmt.Sprintf("  %s:%d", rel, fset.Position(pos).Line))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	// A walk that silently reaches nothing passes forever while enforcing
	// nothing; this tree has hundreds of test files.
	if scanned < 100 {
		t.Fatalf("the walk only reached %d test files — did it stop early?", scanned)
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("tmux started outside a private socket directory:\n%s%s",
			strings.Join(offenders, "\n"), rule)
	}
}

// TestUnguardedSeesAnOffender guards the guard: the matcher must flag a bare
// `tmux -L` and accept the same file once it calls tmuxtest.Private.
func TestUnguardedSeesAnOffender(t *testing.T) {
	body := `exec.Command("tmux", "-L", "x", "kill-server").Run()`
	for _, tc := range []struct {
		name, body string
		flagged    bool
	}{
		{"bare -L", body, true},
		{"bare -S", strings.Replace(body, `"-L"`, `"-S"`, 1), true},
		{"private", "tmuxtest.Private(t)\n" + body, false},
		{"no tmux", `exec.Command("ls", "-L").Run()`, false},
	} {
		src := "package x\n\nfunc f(t T) {\n" + tc.body + "\n}\n"
		file, err := parser.ParseFile(token.NewFileSet(), "x_test.go", src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.name, err)
		}
		if got := unguarded(file) != token.NoPos; got != tc.flagged {
			t.Errorf("%s: flagged = %t, want %t", tc.name, got, tc.flagged)
		}
	}
}

// repoRoot is two levels up from this package, verified by a file only the
// root has, so a moved package fails here instead of quietly scanning nothing.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s — this package moved; fix the relative root: %v", root, err)
	}
	return root
}
