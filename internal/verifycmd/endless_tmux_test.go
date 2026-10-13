package verifycmd

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/mikeschinkel/go-dt"
)

// guardShape is _guard.sh's _endless_guard_runner_config pattern. The two must
// agree, or every suite is refused under the runner; tests/test_suite_guard.py
// drives the bash side.
var guardShape = regexp.MustCompile(`^\{"tmux":\{"socket_dir":"/[^"]*/endless-verify-[^/"]+/endless"\}\}$`)

// TestRunnerTmuxConfigIsWhatTheGuardAccepts writes the config into both
// spellings of the isolated config directory and checks each against the
// guard's shape.
func TestRunnerTmuxConfigIsWhatTheGuardAccepts(t *testing.T) {
	run := t.TempDir()
	home, xdg := filepath.Join(run, "home"), filepath.Join(run, "xdg")
	env := []string{"HOME=" + home, "XDG_CONFIG_HOME=" + xdg}
	tmuxDir := dt.DirPath("/private/tmp/endless-verify-4242")

	if err := writeEndlessTmuxConfig(env, tmuxDir); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		filepath.Join(home, ".config", "endless", "config.json"),
		filepath.Join(xdg, "endless", "config.json"),
	} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		got := string(data[:len(data)-1]) // the guard reads it through $(cat), which drops the newline
		if !guardShape.MatchString(got) {
			t.Errorf("%s = %s, which the guard would refuse", f, got)
		}
		if want := `"socket_dir":"/private/tmp/endless-verify-4242/endless"`; !regexp.MustCompile(regexp.QuoteMeta(want)).MatchString(got) {
			t.Errorf("%s does not point inside the run's tmux dir: %s", f, got)
		}
	}
}
