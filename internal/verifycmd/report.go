package verifycmd

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/verify"
	"github.com/mikeschinkel/go-doterr"
	"github.com/mikeschinkel/go-dt"
)

// fmtCheckReport is the intermediate report filename for check i (where a
// file-emitting runner like pytest writes its native stream).
func fmtCheckReport(i int) string {
	return fmt.Sprintf("check-%d.json", i)
}

// mergeResults merges the per-check CTRF reports into one report (summary counts
// summed, tests concatenated in order) via the E-1604 writer.
func mergeResults(results []checkResult) (merged *verify.Report) {
	var reports []*verify.Report

	reports = make([]*verify.Report, len(results))
	for i, r := range results {
		reports[i] = r.report
	}
	merged = verify.MergeReports(reports...)
	return merged
}

// ReportFileSuffix ends every per-run report's filename, in the cache and
// wherever a caller moves it. The stem in front of it is the run's UTC
// timestamp (ReportTimeLayout), so names sort in run order.
const ReportFileSuffix = ".ctrf.json"

// ReportTimeLayout is the UTC timestamp that names one run's report. Microsecond
// precision, so two runs started in the same second still get two files: an
// earlier run's report is never overwritten by a later one (E-2243).
const ReportTimeLayout = "20060102T150405.000000Z"

// reportDir is the per-task directory every run's report is written into,
// under the user cache dir (~/.cache/endless/verify/<id>/ on Linux). It
// persists across runs and is independent of the ephemeral per-run temp dir.
func reportDir(id string) (dir dt.DirPath, err error) {
	var cacheDir string

	cacheDir, err = os.UserCacheDir()
	if err != nil {
		err = doterr.NewErr(ErrWritingCTRF, err)
		goto end
	}
	dir = dt.DirPath(cacheDir).Join("endless", "verify", id)
end:
	return dir, err
}

// writeCTRF writes the merged CTRF report to a new file named for this run,
// <reportDir>/<UTC timestamp>.ctrf.json, and returns its path (E-2243).
//
// One file per run, never a fixed name: a failed run's report is what a person
// diagnoses from, so a later run must not overwrite it. What happens to the
// files afterwards is not the runner's business — it knows nothing about git.
// `endless task verify` moves a passing run's report onto main and clears the
// task's earlier ones; a failing run's report stays here.
func writeCTRF(id string, r *verify.Report) (fp dt.Filepath, err error) {
	var dir dt.DirPath
	var buf bytes.Buffer

	dir, err = reportDir(id)
	if err != nil {
		goto end
	}
	err = dir.MkdirAll(0o755)
	if err != nil {
		err = doterr.NewErr(ErrWritingCTRF, err, "dir", dir)
		goto end
	}
	err = r.Write(&buf)
	if err != nil {
		err = doterr.NewErr(ErrWritingCTRF, err)
		goto end
	}
	fp = dt.FilepathJoin(dir, time.Now().UTC().Format(ReportTimeLayout)+ReportFileSuffix)
	err = fp.WriteFile(buf.Bytes(), 0o644)
	if err != nil {
		err = doterr.NewErr(ErrWritingCTRF, err, "filepath", fp)
		goto end
	}
end:
	return fp, err
}

// printSummary writes the human-readable pass/fail summary to stdout: one line
// per check, the merged verdict, and — on a failing run only — the CTRF report
// path.
//
// A passing run prints no `CTRF:` line because its report does not stay where
// it was written: `endless task verify` moves it and names where it landed.
// Printing the cache path too would name a file that is gone a moment later.
func printSummary(id string, results []checkResult, merged *verify.Report, ctrfPath dt.Filepath) {
	var s verify.Summary
	var mark, verdict string

	fmt.Printf("verify %s — %d check(s)\n", id, len(results))
	for _, r := range results {
		cs := r.report.Results.Summary
		mark = "✓"
		if cs.Failed > 0 || cs.Tests == 0 {
			mark = "✗"
		}
		fmt.Printf("  %s %-8s %s\n", mark, r.runner, countsLine(cs))
	}

	s = merged.Results.Summary
	fmt.Println("  " + strings.Repeat("─", 5))
	verdict = "PASSED"
	if s.Failed > 0 || s.Tests == 0 {
		verdict = "FAILED"
	}
	fmt.Printf("%s: %s\n", verdict, countsLine(s))
	if verdict == "FAILED" {
		fmt.Printf("CTRF: %s\n", displayPath(string(ctrfPath)))
	}
}

// countsLine renders a summary's counts, failures first when present, always
// ending with the total test count.
func countsLine(s verify.Summary) (line string) {
	var parts []string

	if s.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", s.Failed))
	}
	parts = append(parts, fmt.Sprintf("%d passed", s.Passed))
	if s.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", s.Skipped))
	}
	if s.Pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", s.Pending))
	}
	line = strings.Join(parts, ", ") + fmt.Sprintf(" (%d tests)", s.Tests)
	return line
}
