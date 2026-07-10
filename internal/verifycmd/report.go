package verifycmd

import (
	"bytes"
	"fmt"
	"os"
	"strings"

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

// writeCTRF writes the merged CTRF report to a stable per-task artifact path
// under the user cache dir (~/.cache/endless/verify/<id>/ctrf.json) and returns
// it. This path persists across runs and is independent of the ephemeral per-run
// temp dir, so the summary can always point at the latest report even after the
// temp dir is torn down.
func writeCTRF(id string, r *verify.Report) (fp dt.Filepath, err error) {
	var cacheDir string
	var dir dt.DirPath
	var buf bytes.Buffer

	cacheDir, err = os.UserCacheDir()
	if err != nil {
		err = doterr.NewErr(ErrWritingCTRF, err)
		goto end
	}
	dir = dt.DirPath(cacheDir).Join("endless", "verify", id)
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
	fp = dt.FilepathJoin(dir, "ctrf.json")
	err = fp.WriteFile(buf.Bytes(), 0o644)
	if err != nil {
		err = doterr.NewErr(ErrWritingCTRF, err, "filepath", fp)
		goto end
	}
end:
	return fp, err
}

// printSummary writes the human-readable pass/fail summary to stdout: one line
// per check, the merged verdict, and the CTRF report path.
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
	fmt.Printf("CTRF: %s\n", displayPath(string(ctrfPath)))
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
