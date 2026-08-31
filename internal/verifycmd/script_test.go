package verifycmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
	"github.com/mikeschinkel/go-dt"
)

// enterScriptSuite chdirs into a fresh project holding one script suite, with a
// temp HOME so the CTRF artifact lands in a temp dir and the ownership guard
// finds no main database (fail-open, which is what every test here wants).
func enterScriptSuite(t *testing.T, id, body string) (root string) {
	t.Helper()
	root = t.TempDir()
	dir := filepath.Join(root, verify.SuitesDir, strings.ToLower(id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, verify.ScriptFile), []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	return root
}

// A suite that emits no result stream is judged by its exit code, so a script
// written before any of this existed runs through the front door unchanged.
func TestRun_ScriptSuite_ExitCodeIsTheVerdict(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"passes", "#!/usr/bin/env bash\necho 'ALL PASSED'\nexit 0\n", 0},
		{"fails", "#!/usr/bin/env bash\necho 'one failed'\nexit 1\n", 1},
		{"setup error keeps its own exit code", "#!/usr/bin/env bash\necho 'SETUP ERROR' >&2\nexit 2\n", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enterScriptSuite(t, "E-1", tc.body)
			code, err := run("E-1", false)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if code != tc.want {
				t.Errorf("exit code = %d, want %d (the script's own)", code, tc.want)
			}
		})
	}
}

// The runner exports the TAP destination and the task id, and nothing else
// needs to be true for a harness-sourcing suite to work.
func TestRun_ScriptSuite_ExportsTAPPathAndTaskID(t *testing.T) {
	enterScriptSuite(t, "E-2", `#!/usr/bin/env bash
[[ -n "${ENDLESS_VERIFY_TAP:-}" ]] || exit 11
[[ "${ENDLESS_VERIFY_TASK:-}" == "E-2" ]] || exit 12
exit 0
`)
	code, err := run("E-2", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d (11=TAP missing, 12=TASK wrong)", code)
	}
}

// The runner ADDS no variable that reads as permission.
//
// ENDLESS_VERIFY_RUN was one: a "the runner started you" marker satisfied by a
// single `export`, which is why it is gone. What is asserted here is that
// suiteEnv does not put it back — deliberately NOT that a suite never sees it.
// The runner passes the parent environment through (PATH and the rest), so a
// caller that exports the name still hands it down, and an earlier version of
// this test failed for exactly that reason when an older installed runner
// invoked the newer test binary. A suite inheriting the name is harmless
// because nothing reads it; a runner setting it would not be.
func TestSuiteEnv_AddsNoPermissionGrantingVariable(t *testing.T) {
	root := enterScriptSuite(t, "E-2", "#!/usr/bin/env bash\nexit 0\n")

	env, err := suiteEnv(nil, "E-2", dt.DirPath(root))
	if err != nil {
		t.Fatalf("suiteEnv: %v", err)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "ENDLESS_VERIFY_RUN=") {
			t.Errorf("suiteEnv reintroduced the run marker: %q", kv)
		}
	}
	if len(env) == 0 {
		t.Fatal("suiteEnv returned nothing; the assertion above proves nothing")
	}
}

// A suite that writes TAP gets per-assertion results, not one coarse verdict.
func TestRun_ScriptSuite_NormalizesTAP(t *testing.T) {
	enterScriptSuite(t, "E-3", `#!/usr/bin/env bash
printf 'ok 1 - a\nok 2 - b\nnot ok 3 - c\n' >> "$ENDLESS_VERIFY_TAP"
exit 1
`)
	code, err := run("E-3", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	rpt := readCTRF(t, "E-3")
	if rpt.Results.Summary.Tests != 3 || rpt.Results.Summary.Failed != 1 {
		t.Errorf("summary = %+v, want 3 tests with 1 failure", rpt.Results.Summary)
	}
}

// A report that says everything passed while the process exited non-zero is
// worse than either fact alone. The exit code is reconciled into the report.
func TestRun_ScriptSuite_ReconcilesAnUnexplainedExit(t *testing.T) {
	enterScriptSuite(t, "E-4", `#!/usr/bin/env bash
printf 'ok 1 - a\n' >> "$ENDLESS_VERIFY_TAP"
exit 3
`)
	code, err := run("E-4", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	rpt := readCTRF(t, "E-4")
	if rpt.Results.Summary.Failed == 0 {
		t.Errorf("report claims no failure for a suite that exited 3: %+v", rpt.Results.Summary)
	}
}

// A task holding BOTH forms resolves to the manifest, deterministically. The
// script here would exit 7 if it ran; the manifest passes.
func TestRun_ManifestWinsOverScript(t *testing.T) {
	root := enterScriptSuite(t, "E-5", "#!/usr/bin/env bash\nexit 7\n")
	body := "schema = 1\ntask = \"E-5\"\n[[check]]\nrunner = \"sh\"\ncommand = \"printf '1..1\\nok 1 - m\\n'\"\nformat = \"tap\"\n"
	fp := filepath.Join(root, verify.SuitesDir, "e-5", verify.ManifestFile)
	if err := os.WriteFile(fp, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	code, err := run("E-5", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — the manifest, not the exit-7 script", code)
	}
	rpt := readCTRF(t, "E-5")
	if len(rpt.Results.Tests) != 1 || rpt.Results.Tests[0].Name != "m" {
		t.Errorf("report did not come from the manifest: %+v", rpt.Results.Tests)
	}
}

// A task with NEITHER form names both filenames and the directory they belong
// in, so the error is an instruction rather than a dead end.
func TestRun_NoSuiteNamesBothFilenames(t *testing.T) {
	enterScriptSuite(t, "E-6", "#!/usr/bin/env bash\nexit 0\n")
	_, err := run("E-8", false)
	if err == nil {
		t.Fatal("expected a no-suite error")
	}
	msg := err.Error()
	for _, want := range []string{verify.ManifestFile, verify.ScriptFile, "e-8"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not mention %q: %s", want, msg)
		}
	}
}

// The suite runs under the same isolation the manifest path gets: a temp HOME
// and XDG_CONFIG_HOME, so it cannot read or pollute the real ones.
//
// The property is asserted on the environment's actual shape rather than
// against the old run-dir marker, and it is deliberately the SAME property
// .endless/tasks/_guard.sh refuses on: no Endless config is reachable from
// here. If the runner ever stopped isolating, the guard would start refusing
// every suite, so pinning both to one statement is what keeps that from
// becoming a surprise.
func TestRun_ScriptSuite_RunsIsolated(t *testing.T) {
	enterScriptSuite(t, "E-9", `#!/usr/bin/env bash
[[ -d "$HOME" ]] || exit 20
[[ -d "$XDG_CONFIG_HOME" ]] || exit 21
[[ "$HOME" != "$XDG_CONFIG_HOME" ]] || exit 22
[[ "$(dirname "$HOME")" == "$(dirname "$XDG_CONFIG_HOME")" ]] || exit 23
[[ ! -d "$HOME/.config/endless" ]] || exit 24
[[ ! -d "$XDG_CONFIG_HOME/endless" ]] || exit 25
exit 0
`)
	code, err := run("E-9", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d; the suite reports what was not isolated (20=HOME, 21=XDG, 22=same dir, 23=different parents, 24/25=an endless config is reachable)", code)
	}
}

// readCTRF reads back the merged report the run wrote, which is where the
// normalized results actually land.
func readCTRF(t *testing.T, id string) (rpt *verify.Report) {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("UserCacheDir: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(cache, "endless", "verify", id, "ctrf.json"))
	if err != nil {
		t.Fatalf("read CTRF: %v", err)
	}
	rpt = &verify.Report{}
	if err = json.Unmarshal(data, rpt); err != nil {
		t.Fatalf("parse CTRF: %v", err)
	}
	return rpt
}
