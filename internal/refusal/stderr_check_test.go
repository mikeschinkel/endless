package refusal

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

// rule is appended to the failure. The moment this guard fires is the only
// moment a session is guaranteed to be thinking about the rule, so the rule is
// stated here rather than left in a doc comment nobody opens.
const rule = `

  Every message Endless writes to stderr must say whether the AGENT reading it
  has to report it to the user. A bare write cannot say that, so it is not
  allowed to exist: internal/refusal owns os.Stderr, and its constructors are
  named for the classes, so a site cannot emit a message without choosing one.

  Pick the class from ONE question — can the agent continue without asking the
  user?

    refusal.NoReport(summary, remedy)
        It can. "Do X instead of Y" is this class: the agent retries doing X
        and the user never hears about it.

    refusal.Report(summary, decision)
        It cannot. 'decision' names the judgment that is the user's to make.

    refusal.ReportIf(summary, condition, remedy, decision)
        It depends on something this command cannot see — usually what the user
        asked for. Name both branches; the agent decides.
        Resolve the condition in code wherever you can, and emit a definite
        class instead. The actor, the call path, process ancestry and state
        already in hand answer most of them.

    refusal.Fault(err) / refusal.From(err)
        Endless itself broke. From() is the generic print site: it keeps a
        class the error already carries and faults anything unclassified.

    refusal.Warn(summary, remedy)      nothing is blocked, but say so
    refusal.Info(text)                 a progress notice; no directive

  Then .Exit(code), or .Print() when the caller owns the exit.

  flag.NewFlagSet is forbidden for the same reason, one level down: a plain
  FlagSet writes "flag provided but not defined: -x" and its usage block to
  os.Stderr from inside the flag package, where no check here can see it — and
  flag.ExitOnError prints and exits before the site gets a say at all. Use
  refusal.NewFlags(name); it captures that text, and .Output() hands it back so
  you can classify it and keep a person's bytes unchanged.

  Not a refusal at all:
    - A warning the USER should act on that blocks nothing goes to the errors
      channel — faults.Record — not to stderr. The user sees it on the
      session-status badge and in ` + "`endless errors show`" + `; the agent spends
      nothing on it.
    - A child process whose stderr is its own output: refusal.Passthrough().
    - Diagnostics: refusal.InitLog / refusal.SlogHandler write to the log file.
      A log line a user needs to see is not a log line.
`

// scanRoots are the trees a user-facing message can be written from. Both are
// relative to the repository root, which is two levels up from this package.
var scanRoots = []string{"internal", "cmd"}

// forbidden is the set of package-level names that put text in front of a user
// without a class. Two entries, and they are two halves of one rule:
//
//	os.Stderr        the stream itself, however it is reached — Fprintf, a
//	                 cmd.Stderr assignment, log.SetOutput, an slog handler.
//	flag.NewFlagSet  the stream reached from inside the flag package, where the
//	                 check cannot follow. See refusal.NewFlags.
var forbidden = map[string]bool{
	"os.Stderr":       true,
	"flag.NewFlagSet": true,
}

// TestNoStderrOutsideThisPackage fails on any os.Stderr reference in internal/
// or cmd/ outside internal/refusal (E-2159).
//
// It keys on the IDENTIFIER rather than on the calls that take it, and that is
// the point: fmt.Fprintf(os.Stderr, …), cmd.Stderr = os.Stderr,
// log.SetOutput(os.Stderr), slog.NewTextHandler(os.Stderr, …) and
// fs.SetOutput(os.Stderr) are five spellings of one act, and a check written
// against the spellings would have to grow a sixth row the first time somebody
// invents one. There is exactly one thing to forbid, so forbid that.
//
// Test files are exempt. A test asserting on stderr is not a message to a user,
// and a test that captures os.Stderr to prove a refusal rendered correctly is
// this package's own contract being verified.
func TestNoStderrOutsideThisPackage(t *testing.T) {
	root := repoRoot(t)
	self := filepath.Join(root, "internal", "refusal")

	var offenders []string
	fset := token.NewFileSet()

	for _, dir := range scanRoots {
		start := filepath.Join(root, dir)
		err := filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.IsDir():
				if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
					return fs.SkipDir
				}
				if path == self {
					return fs.SkipDir
				}
				// testdata is not shipped code. A fixture program under it —
				// internal/liveview/testdata/reexecprobe is one — is built by
				// the test that needs it and by nothing else, so a message it
				// writes reaches a test harness rather than a user. `go build
				// ./...` skips these for the same reason.
				if d.Name() == "testdata" {
					return fs.SkipDir
				}
				return nil
			case !strings.HasSuffix(path, ".go"):
				return nil
			case strings.HasSuffix(path, "_test.go"):
				return nil
			}

			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || !forbidden[pkg.Name+"."+sel.Sel.Name] {
					return true
				}
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = path
				}
				offenders = append(offenders, fmt.Sprintf("  %s:%d  %s.%s",
					rel, fset.Position(sel.Pos()).Line, pkg.Name, sel.Sel.Name))
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", start, err)
		}
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("unclassified output to stderr:\n%s%s",
			strings.Join(offenders, "\n"), rule)
	}
}

// TestTheCheckCanSeeAnOffender guards the guard. A walk that silently reaches
// nothing — a renamed directory, a Rel that errors, a parser flag that skips
// the tree — passes forever while enforcing nothing, which is the failure mode
// every source-scanning check has.
func TestTheCheckCanSeeAnOffender(t *testing.T) {
	const src = `package x

import "os"

func f() { os.Stderr.WriteString("x") }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "x.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Stderr" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "os" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("the matcher no longer recognises os.Stderr")
	}
}

// TestTheCheckReachesTheWholeTree pins that the walk actually visits files. It
// counts parsed non-test Go files rather than asserting on a specific one, so
// it survives any rename but not a walk that stops early.
func TestTheCheckReachesTheWholeTree(t *testing.T) {
	root := repoRoot(t)
	count := 0
	for _, dir := range scanRoots {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				count++
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk: %v", err)
		}
	}
	if count < 100 {
		t.Fatalf("the walk only reached %d files — did it stop early?", count)
	}
}

// repoRoot is two levels up from this package, verified by a file that only the
// root has. Deriving it rather than hard-coding a relative path means a moved
// package fails loudly here instead of quietly scanning nothing.
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
