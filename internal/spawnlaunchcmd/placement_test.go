package spawnlaunchcmd

import (
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// TestPlacementFor pins the new-window position each placement becomes
// (E-2234). first and last anchor on the session's {start} and {end} windows,
// never on `<session>:` alone, which means "next free index" and lands in the
// first hole of the numbering; left and right anchor on the active window.
func TestPlacementFor(t *testing.T) {
	cases := []struct {
		place Placement
		want  windowPlacement
	}{
		{PlaceFirst, windowPlacement{"-b", "$3:{start}"}},
		{PlaceLast, windowPlacement{"-a", "$3:{end}"}},
		{PlaceLeft, windowPlacement{"-b", "@9"}},
		{PlaceRight, windowPlacement{"-a", "@9"}},
	}
	for _, tc := range cases {
		if got := placementFor(tc.place, "$3", "@9"); got != tc.want {
			t.Errorf("placementFor(%s) = %v, want %v", tc.place, got, tc.want)
		}
	}
}

// TestParsePlacement accepts the four names and refuses anything else, naming
// the valid ones.
func TestParsePlacement(t *testing.T) {
	for _, p := range Placements {
		got, err := ParsePlacement(string(p))
		if err != nil || got != p {
			t.Errorf("ParsePlacement(%q) = %q, %v", p, got, err)
		}
	}
	for _, bad := range []string{"", "middle", "First"} {
		if _, err := ParsePlacement(bad); err == nil || !strings.Contains(err.Error(), `"right"`) {
			t.Errorf("ParsePlacement(%q) err = %v, want a refusal naming the valid values", bad, err)
		}
	}
}

// TestSessionLookupArgs pins the exact-match `=` on a session name: without it
// tmux matches a prefix and then a glob, and `--tmux-session end` would land in
// a session called `endless`.
func TestSessionLookupArgs(t *testing.T) {
	if got, want := hasSessionArgs("work"), []string{"has-session", "-t", "=work"}; !reflect.DeepEqual(got, want) {
		t.Errorf("hasSessionArgs = %q, want %q", got, want)
	}
	if got, want := namedSessionIDArgs("work"), []string{"display-message", "-p", "-t", "=work:", "#{session_id}"}; !reflect.DeepEqual(got, want) {
		t.Errorf("namedSessionIDArgs = %q, want %q", got, want)
	}
	if got, want := activeWindowArgs("$3"), []string{"display-message", "-p", "-t", "$3:", "#{window_id}"}; !reflect.DeepEqual(got, want) {
		t.Errorf("activeWindowArgs = %q, want %q", got, want)
	}
}

// stubTmux replaces both exec wrappers with one fake answering by verb.
func stubTmux(t *testing.T, answer func(args []string) (string, error)) *[][]string {
	t.Helper()
	var calls [][]string
	run, runOut := tmuxRun, tmuxRunOut
	t.Cleanup(func() { tmuxRun, tmuxRunOut = run, runOut })
	tmuxRunOut = func(args ...string) (string, error) {
		calls = append(calls, args)
		return answer(args)
	}
	tmuxRun = func(args ...string) error {
		_, err := tmuxRunOut(args...)
		return err
	}
	return &calls
}

// TestResolveTarget_NamedSession: --tmux-session is checked with has-session,
// then resolved to its id, and the window is placed in THAT session.
func TestResolveTarget_NamedSession(t *testing.T) {
	t.Setenv("TMUX_PANE", "")
	stubTmux(t, func(args []string) (string, error) {
		switch args[0] {
		case "has-session":
			return "", nil
		case "display-message":
			if args[4] == "#{session_id}" {
				return "$5", nil
			}
			return "@12", nil
		}
		return "", errors.New("unexpected")
	})
	got, err := resolveTarget("", "work", PlaceRight)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if want := (windowPlacement{"-a", "@12"}); got != want {
		t.Errorf("resolveTarget = %v, want %v", got, want)
	}
	got, err = resolveTarget("", "work", PlaceLast)
	if err != nil || got != (windowPlacement{"-a", "$5:{end}"}) {
		t.Errorf("resolveTarget last = %v, %v", got, err)
	}
}

// TestResolveTarget_MissingNamedSessionRefuses: a name no session has is
// refused before anything is created, and the refusal names it.
func TestResolveTarget_MissingNamedSessionRefuses(t *testing.T) {
	calls := stubTmux(t, func(args []string) (string, error) {
		return "", errors.New("can't find session")
	})
	_, err := resolveTarget("", "nope", PlaceFirst)
	if err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("err = %v, want a refusal naming the session", err)
	}
	if len(*calls) != 1 || (*calls)[0][0] != "has-session" {
		t.Errorf("calls = %q, want only the has-session check", *calls)
	}
}

// TestResolveTarget_OnlyRelativeAsksForActiveWindow: first and last need no
// active-window query; left and right do.
func TestResolveTarget_OnlyRelativeAsksForActiveWindow(t *testing.T) {
	calls := stubTmux(t, func(args []string) (string, error) { return "@1", nil })
	for _, p := range []Placement{PlaceFirst, PlaceLast} {
		if _, err := resolveTarget("$2", "", p); err != nil {
			t.Fatal(err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("first/last queried tmux: %q", *calls)
	}
	if _, err := resolveTarget("$2", "", PlaceLeft); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0], activeWindowArgs("$2")) {
		t.Errorf("left calls = %q", *calls)
	}
}

// TestPlacement_LiveTmux runs each placement against a scratch tmux server
// (`tmux -L`), so tmux itself — not just the argv — is what is checked: where
// the window's tab lands, and that a detached (`--no-refocus`) spawn leaves the
// previously active window active. The session's numbering has a hole (0, 2)
// because that is where `<session>:` alone went wrong.
func TestPlacement_LiveTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	sock := "endless-e2234-" + strings.ReplaceAll(t.Name(), "/", "-")
	tm := func(args ...string) (string, error) {
		out, err := exec.Command("tmux", append([]string{"-L", sock, "-f", "/dev/null"}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	must := func(args ...string) string {
		t.Helper()
		out, err := tm(args...)
		if err != nil {
			t.Fatalf("tmux %q: %v: %s", args, err, out)
		}
		return out
	}
	t.Cleanup(func() { _, _ = tm("kill-server") })

	run, runOut := tmuxRun, tmuxRunOut
	t.Cleanup(func() { tmuxRun, tmuxRunOut = run, runOut })
	tmuxRunOut = tm
	tmuxRun = func(args ...string) error { _, err := tm(args...); return err }

	// Windows A(0) B(1) C(2), then B killed: A(0) C(2), A active.
	must("new-session", "-d", "-s", "probe", "-n", "A", "-x", "80", "-y", "24")
	must("new-window", "-d", "-t", "probe:1", "-n", "B")
	must("new-window", "-d", "-t", "probe:2", "-n", "C")
	must("kill-window", "-t", "probe:B")
	layout := func() string {
		return must("list-windows", "-t", "=probe", "-F", "#{window_name}#{?window_active,*,}")
	}

	steps := []struct {
		place Placement
		name  string
		want  string
	}{
		{PlaceFirst, "FIRST", "FIRST A* C"},
		{PlaceLast, "LAST", "FIRST A* C LAST"},
		{PlaceRight, "RIGHT", "FIRST A* RIGHT C LAST"},
		{PlaceLeft, "LEFT", "FIRST LEFT A* RIGHT C LAST"},
	}
	for _, st := range steps {
		wp, err := resolveTarget("", "probe", st.place)
		if err != nil {
			t.Fatalf("%s: resolveTarget: %v", st.place, err)
		}
		if _, err = tmuxRunOut(newWindowArgs(wp, "", st.name, true, []string{"sleep", "60"})...); err != nil {
			t.Fatalf("%s: new-window: %v", st.place, err)
		}
		if got := strings.ReplaceAll(layout(), "\n", " "); got != st.want {
			t.Errorf("after --to-%s: windows = %q, want %q", st.place, got, st.want)
		}
	}

	// Without -d the new window takes focus — the plain manual spawn.
	wp, err := resolveTarget("", "probe", PlaceFirst)
	if err != nil {
		t.Fatal(err)
	}
	must(newWindowArgs(wp, "", "FOCUS", false, []string{"sleep", "60"})...)
	if got := strings.ReplaceAll(layout(), "\n", " "); !strings.HasPrefix(got, "FOCUS* ") {
		t.Errorf("refocusing spawn: windows = %q, want FOCUS first and active", got)
	}

	// A prefix of a real session's name is not that session.
	if _, err = resolveTarget("", "prob", PlaceFirst); err == nil {
		t.Error(`--tmux-session "prob" resolved against session "probe"; want a refusal`)
	}
}
