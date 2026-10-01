package refusal

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/agentenv"
)

// goldenPath is deliberately outside internal/refusal/testdata.
//
// The file is a contract between the two HALVES of Endless, not a fixture for
// one package: tests/test_refusal_verdicts.py asserts the same cases against
// the Python constructors. A Go-only testdata copy would let the two renderings
// drift by exactly one edit, which is the failure this whole task exists to
// end — one refusal, worded two ways, depending on which language happened to
// raise it.
const goldenPath = "../../tests/fixtures/refusal-verdicts.json"

// goldenCase is one class, its constructor inputs, and the verdict they must
// produce. Fields match the JSON keys in the golden file.
type goldenCase struct {
	Note      string `json:"note"`
	Class     string `json:"class"`
	Command   string `json:"command"`
	Summary   string `json:"summary"`
	Remedy    string `json:"remedy"`
	Condition string `json:"condition"`
	Decision  string `json:"decision"`
	Verdict   string `json:"verdict"`
}

// build reconstructs the *Error a site would have written.
func (c goldenCase) build(t *testing.T) *Error {
	t.Helper()
	switch Class(c.Class) {
	case ClassNoReport:
		return NoReport(c.Summary, c.Remedy).Command(c.Command)
	case ClassReport:
		return Report(c.Summary, c.Decision).Command(c.Command)
	case ClassReportIf:
		return ReportIf(c.Summary, c.Condition, c.Remedy, c.Decision).Command(c.Command)
	case ClassFault:
		return Fault(errors.New(c.Summary)).Command(c.Command)
	case ClassWarn:
		return Warn(c.Summary, c.Remedy).Command(c.Command)
	case ClassInfo:
		return Info(c.Summary).Command(c.Command)
	}
	t.Fatalf("golden case names an unknown class %q", c.Class)
	return nil
}

func loadGolden(t *testing.T) []goldenCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(goldenPath))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var cases []goldenCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("golden file is empty")
	}
	return cases
}

// asAgent makes Agent() true without depending on whatever harness is running
// the test.
func asAgent(t *testing.T) {
	t.Helper()
	asHuman(t)
	t.Setenv(AudienceVar, AudienceAgent)
}

// asHuman clears every signal Agent() keys on. The harness variables matter:
// this suite is normally run BY an agent, so "no agent" is a state the test has
// to construct rather than assume.
func asHuman(t *testing.T) {
	t.Helper()
	t.Setenv(AudienceVar, "")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")
	t.Setenv("__CFBundleIdentifier", "")
	if agentenv.Present() {
		t.Fatal("asHuman: a harness is still detected; agentenv gained a signal this helper does not clear")
	}
}

// TestGoldenVerdicts pins one rendered verdict per class (E-2159).
//
// It asserts the VERDICT rather than the whole rendering because the verdict is
// the part with a contract: it is what survives a truncating pipe, and it is
// what the directive rides on. The body is whatever the site has always
// printed, and is asserted per-site by that site's own test.
func TestGoldenVerdicts(t *testing.T) {
	asAgent(t)
	for _, c := range loadGolden(t) {
		t.Run(c.Class, func(t *testing.T) {
			e := c.build(t)
			got := e.Verdict()
			if Class(c.Class) == ClassInfo {
				// Info carries no verdict; what the golden pins is the text
				// itself, unchanged, which is the whole of the class.
				got = e.Render()
			}
			if got != c.Verdict {
				t.Errorf("verdict mismatch\n want: %s\n  got: %s", c.Verdict, got)
			}
		})
	}
}

// TestHumanRenderingIsTodaysText holds the half of the contract that is easy to
// lose: a person's stderr does not change. The directive is ADDITIVE, and it is
// added for one reader only.
func TestHumanRenderingIsTodaysText(t *testing.T) {
	asHuman(t)
	cases := []struct {
		name string
		err  *Error
		want string
	}{
		{
			"the agent's remedy is not added to a person's message",
			NoReport("title 107>100 chars. Nothing was created.", "Move the long form to --analysis and retry").Command("task add"),
			"title 107>100 chars. Nothing was created.",
		},
		{
			"report carries no remedy, because there is nothing to do but ask",
			Report("E-101's worktree is in use by a live session.", "whether to remove it anyway").Command("worktree drop"),
			"E-101's worktree is in use by a live session.",
		},
		{
			"Text pins today's bytes when the message does not decompose",
			NoReport("flag provided but not defined: -x", "Fix the flag and retry").
				Text("flag provided but not defined: -x\nUsage of in-use:\n  -dir string\n"),
			"flag provided but not defined: -x\nUsage of in-use:\n  -dir string",
		},
		{
			"fault reads as the error it wraps",
			Fault(errors.New("endless-go returned no output")).Command("session list"),
			"endless-go returned no output",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.err.Render(); got != c.want {
				t.Errorf("human rendering\n want: %q\n  got: %q", c.want, got)
			}
		})
	}
}

// TestHumanRemedyNeverReachesAnAgent is the rule that makes REPORT mean
// anything. A refusal that names its own bypass is a refusal an agent routes
// around, so --force and the destructive git escapes are human-only.
func TestHumanRemedyNeverReachesAnAgent(t *testing.T) {
	const bypass = "Pass --force to remove it anyway."
	build := func() *Error {
		return Report("E-101's worktree is in use by a live session.", "whether to remove it anyway").
			Command("worktree drop").
			HumanRemedy(bypass)
	}

	asHuman(t)
	const todaysText = "E-101's worktree is in use by a live session. Pass --force to remove it anyway."
	if got := build().Render(); got != todaysText {
		t.Errorf("a human reads today's message unchanged\n want: %q\n  got: %q", todaysText, got)
	}

	asAgent(t)
	if got := build().Render(); strings.Contains(got, "--force") {
		t.Errorf("an agent must not be offered the bypass; got %q", got)
	}
}

// TestBracketOnlyWhenTruncationCanSplitIt: the repeated verdict buys nothing
// for a message head and tail both yield whole.
func TestBracketOnlyWhenTruncationCanSplitIt(t *testing.T) {
	asAgent(t)

	oneLiner := NoReport("--dir is required.", "Pass --dir and retry").Command("worktree in-use")
	if got := oneLiner.Render(); strings.Contains(got, "\n") {
		t.Errorf("a one-line refusal renders as the verdict alone; got %q", got)
	}

	withBody := NoReport("no verb given.", "Pass a verb and retry").
		Command("worktree").
		Detail("usage: endless-go worktree <verb>\n  in-use\n  ledger-orphans")
	lines := strings.Split(withBody.Render(), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected a bracketed rendering; got %q", withBody.Render())
	}
	if lines[0] != lines[len(lines)-1] {
		t.Errorf("the two verdict lines must be byte-identical\n first: %q\n  last: %q",
			lines[0], lines[len(lines)-1])
	}
	if !strings.HasPrefix(lines[0], Sentinel) {
		t.Errorf("the bracket's first line must be the verdict; got %q", lines[0])
	}
}

// TestFromClassifiesAnUnclassifiedError pins decision 4: an error that reached
// a print site without a class is a FAULT, not a guess.
func TestFromClassifiesAnUnclassifiedError(t *testing.T) {
	if got := From(errors.New("boom")).Class(); got != ClassFault {
		t.Errorf("an unclassified error must render as a fault; got %q", got)
	}
	classified := NoReport("nothing changed.", "retry with --force")
	if got := From(classified); got != classified {
		t.Error("From must preserve the class its site already chose")
	}
	wrapped := errors.Join(errors.New("context"), classified)
	if got := From(wrapped).Class(); got != ClassNoReport {
		t.Errorf("From must find a class through a wrap; got %q", got)
	}
}

// TestHookAudienceIgnoresTheEnvironment: a hook's reader is decided by the
// event and the exit path, never by who typed the command that triggered it.
func TestHookAudienceIgnoresTheEnvironment(t *testing.T) {
	asHuman(t)
	blocked := NoReport("this session has not declared a task.", "Run `endless task claim <id>`").
		Command("PreToolUse").
		ToAgent()
	if !strings.HasPrefix(blocked.Render(), Sentinel) {
		t.Errorf("a block reason is read by the model even at a human's terminal; got %q", blocked.Render())
	}

	asAgent(t)
	notice := Fault(errors.New("session start failed")).Command("SessionStart").ToHuman()
	if strings.HasPrefix(notice.Render(), Sentinel) {
		t.Errorf("SessionStart stderr is read by the person; got %q", notice.Render())
	}
}

// TestErrorImplementsError keeps a classified refusal classified across a
// return boundary — the point of implementing error at all.
func TestErrorImplementsError(t *testing.T) {
	asHuman(t)
	cause := errors.New("no such file")
	var err error = Fault(cause).Command("verify")
	if !errors.Is(err, cause) {
		t.Error("the cause must remain reachable through errors.Is")
	}
	if err.Error() != "no such file" {
		t.Errorf("Error() is the human rendering; got %q", err.Error())
	}
}

// TestSettersDoNotMutateAShared Value: a refusal is often a package-level value
// reached through From() at a print site, which then names its command. If the
// setters mutated, one caller's verb would be written onto a value every other
// caller shares — and in the session monitor, which runs for hours, the wrong
// verb would be printed from then on.
func TestSettersDoNotMutateASharedValue(t *testing.T) {
	asHuman(t)
	shared := NoReport("--db requires a value: main or sandbox",
		"Pass --db main or --db sandbox and retry")

	first := shared.Command("event emit").Text("endless-go event emit: --db requires a value")
	second := shared.Command("session-query list-live")

	if shared.command != "" {
		t.Errorf("the shared value learned a command: %q", shared.command)
	}
	if shared.text != "" {
		t.Errorf("the shared value learned a text: %q", shared.text)
	}
	if first.command == second.command {
		t.Errorf("two callers ended up with one command: %q", first.command)
	}
	if second.text != "" {
		t.Errorf("a later caller inherited an earlier caller's text: %q", second.text)
	}
}

// TestVerdictDoesNotRepeatTheBinaryPrefix: a Go message opens with
// "endless-go <verb>: " and the verdict opens with "[Endless] <verb>: ". Both
// belong where they are; printing them adjacent does not.
func TestVerdictDoesNotRepeatTheBinaryPrefix(t *testing.T) {
	cases := []struct {
		summary string
		command string
		want    string
	}{
		{"endless-go worktree in-use: --dir is required", "worktree in-use",
			"[Endless] worktree in-use: --dir is required"},
		{"endless-go: unknown subcommand \"x\"", "endless-go",
			"[Endless] endless-go: unknown subcommand \"x\""},
		{"endless-migrate: no command given", "endless-migrate",
			"[Endless] endless-migrate: no command given"},
		// Not a prefix: a message that merely mentions the binary keeps it.
		{"reinstall endless-go and retry", "jobs",
			"[Endless] jobs: reinstall endless-go and retry"},
		// Python-side messages carry no prefix and are untouched.
		{"title 107>100 chars.", "task add",
			"[Endless] task add: title 107>100 chars."},
	}
	for _, c := range cases {
		got := Fault(errors.New(c.summary)).Command(c.command).Verdict()
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("verdict\n want prefix: %s\n         got: %s", c.want, got)
		}
	}
}

// TestVerdictDoesNotRestateTheCommand: a site that names its own verb in the
// summary — "spawn-window: --pane is required" — is writing the line a person
// reads. The verdict opens with the verb too, and both at once is the same
// words twice in a line whose point is density.
func TestVerdictDoesNotRestateTheCommand(t *testing.T) {
	got := NoReport("spawn-window: --pane is required", "Pass --pane and retry").
		Command("spawn-window").Verdict()
	want := "[Endless] spawn-window: --pane is required"
	if !strings.HasPrefix(got, want) {
		t.Errorf("verdict\n want prefix: %s\n         got: %s", want, got)
	}
	// A verb that merely appears mid-summary is not a prefix and stays put.
	kept := NoReport("retry spawn-window: later", "wait").Command("jobs").Verdict()
	if !strings.Contains(kept, "retry spawn-window: later") {
		t.Errorf("a mid-summary mention was stripped: %s", kept)
	}
}
