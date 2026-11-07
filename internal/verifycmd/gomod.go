package verifycmd

import (
	"fmt"
	"strings"

	"github.com/mikeschinkel/endless/internal/gomodcheck"
	"github.com/mikeschinkel/endless/internal/verify"
	"github.com/mikeschinkel/go-dt"
)

// goModCheckLabel names the built-in row that checks a Go module's go.mod is
// tidy as the main checkout, which has no go.work, sees it (E-2569). The root
// module's row is the bare label; a nested module's appends its directory.
const goModCheckLabel = "go.mod is tidy without go.work"

// goModCheckTool is the producer name the row's CTRF report carries.
const goModCheckTool = "go mod tidy"

// runGoModChecks is the built-in first check of every run in a worktree that
// holds a Go module: one row per module found, each `go mod tidy -diff` under
// GOWORK=off on a copy of its go.mod re-rooted to the main checkout, so an
// untidy go.mod fails here, in the worktree, and not main's build after the
// land. It returns no rows for a worktree with no Go module, and none when the
// main checkout root has a go.work of its own: main then builds in workspace
// mode too, so nothing is masked relative to it, and a strict check would
// refuse a go.mod main accepts.
//
// The rows are results, not refusals: a failing one is reported first in the
// summary and the CTRF like any other check, and the suite's own checks still
// run, exactly as a failing first native test is reported.
func runGoModChecks(root dt.DirPath, env []string) (results []checkResult, err error) {
	var mods []string
	var main dt.DirPath
	var hasWork bool
	var res gomodcheck.Result

	mods, err = gomodcheck.Modules(root)
	if err != nil || len(mods) == 0 {
		goto end
	}
	main, err = gomodcheck.MainCheckout(root)
	if err != nil {
		goto end
	}
	hasWork, err = dt.FilepathJoin(main, gomodcheck.GoWorkFile).Exists()
	if err != nil || hasWork {
		goto end
	}

	for _, mod := range mods {
		label := goModCheckLabel
		if mod != "." {
			label = fmt.Sprintf("%s (%s)", goModCheckLabel, mod)
		}
		res, err = gomodcheck.Run(root.Join(mod), main.Join(mod), env)
		if err != nil {
			err = NewErr(err, "check", label)
			goto end
		}
		results = append(results, goModRow(label, mod, res))
	}
end:
	if err != nil {
		results = nil
	}
	return results, err
}

// goModRow renders one module's verdict as a check row: a passing one-test
// report, or a failing one whose message carries what tidy printed and the
// remedy.
func goModRow(label, mod string, res gomodcheck.Result) (row checkResult) {
	var rpt *verify.Report
	var message string

	switch {
	case res.OldGo != "":
		message = goModOldGo(res.OldGo)
	case !res.Tidy:
		message = goModFailure(mod, res)
	}
	rpt = verify.OneTestReport(goModCheckTool, label, message)
	rpt.LabelTests(label)
	row = checkResult{label: label, report: rpt}
	return row
}

// goModOldGo is the failing row's message when the `go` on PATH predates the
// -diff flag. Too old a toolchain is the row's verdict, not a skip and not an
// abort: skipping would reinstate the mask silently, and aborting would hide
// the suite's other rows behind one the user may not be able to fix right now.
func goModOldGo(found string) (message string) {
	return fmt.Sprintf("go mod tidy -diff needs %s or later, and the go on PATH is %s.\n\nFix: install %s or later, or put it first on PATH.",
		gomodcheck.MinGoVersion, found, gomodcheck.MinGoVersion)
}

// goModFailure is the failing row's message: why, what tidy said, and the
// remedy. The remedy names the module's directory, and when relative replace
// paths were re-rooted for the run it says so, because a plain `go mod tidy`
// in the worktree would stop at those paths before it reached the drift.
func goModFailure(mod string, res gomodcheck.Result) (message string) {
	var b strings.Builder
	var where string

	b.WriteString("go.mod or go.sum would change under GOWORK=off, which is how the main checkout builds:\n")
	if diff := strings.TrimSpace(res.Diff); diff != "" {
		b.WriteString("\n" + diff + "\n")
	}
	if res.Detail != "" {
		b.WriteString("\n" + res.Detail + "\n")
	}
	where = "the worktree root"
	if mod != "." {
		where = mod + "/"
	}
	fmt.Fprintf(&b, "\nFix: run `go mod tidy` in %s, then commit go.mod and go.sum.", where)
	if len(res.Rerooted) > 0 {
		fmt.Fprintf(&b, "\nThis module's relative replace paths (%s) resolve from the main checkout's location, not the worktree's, so run it from that depth.",
			strings.Join(res.Rerooted, ", "))
	}
	message = b.String()
	return message
}
