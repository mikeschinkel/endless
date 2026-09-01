package projectstatuscmd

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/go-cfgstore"
)

// init wires cfgstore's package-global logger, which it PANICS without. The
// production binary sets it in main(); tests have no such entry point. A discard
// handler is sufficient — these tests assert on return values, not log output.
// Same pattern as internal/monitor/session_lifecycle_test.go.
func init() {
	cfgstore.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestDefaultNameLeadsWithTheProject is the naming rule, and it is a rule about
// the FIRST NINE characters because that is all a tmux status line shows on the
// machine this was reported from.
//
// Two shapes failed it before this one, and both looked fine in `tmux ls`:
// `{{project}}-monitor` showed `endless-m`, a mangled twin of the user's own
// `endless` session sitting beside it in the list; and a fixed `e-monitor` with
// a `-{{project}}` suffix for the second project showed `e-monitor` for both,
// which is the wrong-board problem the suffix existed to prevent, moved one
// level down.
func TestDefaultNameLeadsWithTheProject(t *testing.T) {
	const tabWidth = 9

	tests := []struct{ project, want, tab string }{
		{"endless", "e-endless-monitor", "e-endless"},
		{"gomion", "e-gomion-monitor", "e-gomion-"},
		// A short project name keeps enough room that "monitor" survives into
		// the tab as well.
		{"h2pp", "e-h2pp-monitor", "e-h2pp-mo"},
		// A user-chosen name with tmux's forbidden characters in it is folded on
		// the way out, so the user never has to know tmux's rules.
		{"My Cool App", "e-My-Cool-App-monitor", "e-My-Coo"},
	}
	for _, tt := range tests {
		got, warn := sessionNameFor(tt.project, "")
		if warn != nil {
			t.Errorf("sessionNameFor(%q, default) warned: %v", tt.project, warn)
		}
		if got != tt.want {
			t.Errorf("sessionNameFor(%q) = %q, want %q", tt.project, got, tt.want)
		}
		if tab := got[:min(tabWidth, len(got))]; !strings.HasPrefix(tab, tt.tab) {
			t.Errorf("sessionNameFor(%q) shows %q in %d columns, want %q",
				tt.project, tab, tabWidth, tt.tab)
		}
	}

	// The property those cases are instances of: two projects must differ INSIDE
	// the truncated window, or the tab cannot tell them apart.
	a, _ := sessionNameFor("endless", "")
	b, _ := sessionNameFor("gomion", "")
	if a[:tabWidth] == b[:tabWidth] {
		t.Errorf("two projects share the visible %d columns: both read %q", tabWidth, a[:tabWidth])
	}

	// ...and the board must not read as the project's OWN session, which is the
	// collision `endless-m` created.
	if strings.HasPrefix(a, "endless") {
		t.Errorf("the monitor session reads as the project's own session: %q", a)
	}
}

// TestSessionNameTemplateForms: `{{project}}` is the form the setting is written
// in, and `{{.Project}}` resolves to the same string for anyone who prefers Go's
// data syntax. A user should not have to know which one this happens to support.
func TestSessionNameTemplateForms(t *testing.T) {
	fn, warnA := sessionNameFor("endless", "{{project}}-board")
	dot, warnB := sessionNameFor("endless", "{{.Project}}-board")
	if warnA != nil || warnB != nil {
		t.Fatalf("a legal template warned: %v / %v", warnA, warnB)
	}
	if fn != "endless-board" || dot != "endless-board" {
		t.Fatalf("the two forms disagree: {{project}} = %q, {{.Project}} = %q", fn, dot)
	}
}

// TestSessionNameIsFoldedAfterRendering: the fold applies to the RENDERED
// result, not to the substituted project alone. A template may carry its own
// spaces and punctuation, and folding only the substitution would leave those
// for tmux to reject later, far from the setting that caused it.
func TestSessionNameIsFoldedAfterRendering(t *testing.T) {
	got, warn := sessionNameFor("endless", "my board.{{project}}:live")
	if warn != nil {
		t.Fatalf("a legal template warned: %v", warn)
	}
	if got != "my-board-endless-live" {
		t.Errorf("rendered name was not folded to a legal tmux name: %q", got)
	}
	if strings.ContainsAny(got, " .:/") {
		t.Errorf("rendered name still holds a character tmux refuses: %q", got)
	}
}

// TestBadTemplateFallsBackAndSaysSo: a typo in a preference must not be able to
// stop the board from opening. It falls back to the built-in name and returns
// the reason, which the launcher prints.
func TestBadTemplateFallsBackAndSaysSo(t *testing.T) {
	for _, tmpl := range []string{
		"{{project",          // unclosed action
		"{{ nosuchfunc }}",   // unknown function
		"{{.NoSuchField}}",   // unknown field
		"{{project}}{{end}}", // unbalanced
		"   ",                // renders to nothing
	} {
		got, warn := sessionNameFor("endless", tmpl)
		if got != "e-endless-monitor" {
			t.Errorf("template %q did not fall back to the default: got %q", tmpl, got)
		}
		if tmpl != "   " && warn == nil {
			t.Errorf("template %q fell back silently; the user is owed the reason", tmpl)
		}
	}
}

// TestBlankTemplateIsTheDefault: an unset setting is not an error and not an
// empty name — it is "no preference expressed".
func TestBlankTemplateIsTheDefault(t *testing.T) {
	got, warn := sessionNameFor("endless", "")
	if warn != nil || got != "e-endless-monitor" {
		t.Fatalf("an unset template did not yield the default: %q (%v)", got, warn)
	}
}

// TestSessionNameTemplateReadsProjectConfig drives the real layered loader
// against a throwaway project directory, so what is tested is that the setting
// is actually WIRED — a template that renders correctly but is never read would
// pass every test above.
func TestSessionNameTemplateReadsProjectConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".endless"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := `{"tmux": {"session_name": "{{project}}-board"}}`
	if err := os.WriteFile(filepath.Join(dir, ".endless", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if got := sessionNameTemplate(dir); got != "{{project}}-board" {
		t.Fatalf("sessionNameTemplate = %q, want the project layer's value", got)
	}
	name, warn := sessionNameFor("endless", sessionNameTemplate(dir))
	if warn != nil {
		t.Fatalf("configured template warned: %v", warn)
	}
	if name != "endless-board" {
		t.Errorf("configured template produced %q, want %q", name, "endless-board")
	}
}

// TestSessionNameTemplateSurvivesABrokenConfig: a config file that cannot be
// parsed must not stop the board from opening. The setting is a preference and
// the built-in name is a working answer; refusing to open a window over a stray
// comma would trade the feature for the preference.
func TestSessionNameTemplateSurvivesABrokenConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".endless"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".endless", "config.json"),
		[]byte("{not json"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	name, _ := sessionNameFor("endless", sessionNameTemplate(dir))
	if name != "e-endless-monitor" {
		t.Errorf("a broken config did not fall back to the default name: %q", name)
	}
}

// TestSessionNameTemplateWithNoConfig: an unconfigured project is the ordinary
// case, and must read as "no preference" rather than as an error.
func TestSessionNameTemplateWithNoConfig(t *testing.T) {
	if got := sessionNameTemplate(t.TempDir()); got != "" {
		t.Errorf("an unconfigured project yielded %q, want the empty (inherit) value", got)
	}
}
