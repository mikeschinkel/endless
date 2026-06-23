package verify

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"

	"github.com/mikeschinkel/go-doterr"
)

// tapLineRE matches a TAP test line: an optional "not ", the literal ok, an
// optional test number, an optional "- " separator, and the trailing
// description (which may carry a directive). The \b after ok keeps "okay ..."
// from matching.
var tapLineRE = regexp.MustCompile(`^(not )?ok\b[ \t]*([0-9]+)?[ \t]*(?:- )?(.*)$`)

// tapDirectiveRE matches a TAP directive (# SKIP / # TODO, case-insensitive) at
// the end of a description, capturing the keyword and any reason.
var tapDirectiveRE = regexp.MustCompile(`(?i)#\s*(SKIP|TODO)\b\s*(.*)$`)

// parseTAP normalizes a TAP (Test Anything Protocol, v13/v14) stream, used for
// shell/BATS suites. Each "ok"/"not ok" line is one test: ok -> passed, not ok
// -> failed, a # SKIP directive -> skipped, a # TODO directive -> pending
// (declared-but-unimplemented). A following YAML block (--- ... ...) or
// diagnostic (#) lines attach to the most recent test as Trace/Message. TAP
// carries no per-test timing, so Duration is 0 and the summary times are unset.
func parseTAP(raw []byte) (rpt *Report, err error) {
	var (
		sc      = bufio.NewScanner(bytes.NewReader(raw))
		tests   []Test
		curIdx  = -1
		inYAML  bool
		yaml    strings.Builder
		line    string
		trimmed string
		diag    string
	)

	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line = sc.Text()
		trimmed = strings.TrimSpace(line)

		if inYAML {
			if trimmed == "..." {
				inYAML = false
				if curIdx >= 0 {
					tests[curIdx].Trace = strings.TrimRight(yaml.String(), "\n")
				}
				yaml.Reset()
				continue
			}
			yaml.WriteString(line)
			yaml.WriteByte('\n')
			continue
		}

		switch {
		case trimmed == "---" && curIdx >= 0:
			inYAML = true
		case tapLineRE.MatchString(trimmed):
			tests = append(tests, parseTAPLine(trimmed))
			curIdx = len(tests) - 1
		case strings.HasPrefix(trimmed, "#") && curIdx >= 0:
			diag = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			if tests[curIdx].Message != "" {
				diag = tests[curIdx].Message + "\n" + diag
			}
			tests[curIdx].Message = diag
		default:
			// Plan line (1..N), version line, blanks: nothing to record.
		}
	}

	err = sc.Err()
	if err != nil {
		err = doterr.NewErr(ErrParsingTAP, err)
		rpt = nil
		goto end
	}

	rpt = &Report{}
	rpt.Results.Tool.Name = "tap"
	rpt.Results.Tests = tests
	rpt.Results.Summary = newSummary(tests)

end:
	return rpt, err
}

// parseTAPLine builds a Test from a single TAP test line. A trailing # SKIP or
// # TODO directive overrides the ok/not-ok status and is stripped from the name.
func parseTAPLine(line string) (t Test) {
	var (
		m         = tapLineRE.FindStringSubmatch(line)
		notOK     = m[1] != ""
		desc      = m[3]
		directive []string
	)

	t.Status = StatusPassed
	if notOK {
		t.Status = StatusFailed
	}

	directive = tapDirectiveRE.FindStringSubmatch(desc)
	if directive != nil {
		desc = strings.TrimSpace(tapDirectiveRE.ReplaceAllString(desc, ""))
		switch strings.ToUpper(directive[1]) {
		case "SKIP":
			t.Status = StatusSkipped
		case "TODO":
			t.Status = StatusPending
		}
	}

	t.Name = strings.TrimSpace(desc)
	return t
}
