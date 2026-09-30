package sessionmonitorcmd

import (
	"slices"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/upid"
)

func TestTagArgs(t *testing.T) {
	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{"tag", tagArgs("%7", "123@456"), []string{"set-option", "-p", "-t", "%7", "@endless_session_monitor", "123@456"}},
		{"untag", untagArgs("%7"), []string{"set-option", "-p", "-u", "-t", "%7", "@endless_session_monitor"}},
		{"read", readTagArgs("%7"), []string{"show-options", "-p", "-v", "-t", "%7", "@endless_session_monitor"}},
	}
	for _, c := range cases {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestResolveScope(t *testing.T) {
	cases := []struct {
		name    string
		session string
		all     bool
		pane    string
		want    scope
		wantErr string
	}{
		{name: "current", pane: "%3", want: scope{target: "%3", label: "this tmux session"}},
		{name: "named", session: "active", pane: "%3", want: scope{target: "=active", label: `tmux session "active"`}},
		{name: "named outside tmux", session: "active", want: scope{target: "=active", label: `tmux session "active"`}},
		{name: "all", all: true, want: scope{all: true, label: "any tmux session"}},
		{name: "no tmux", wantErr: "not inside tmux"},
		{name: "both", session: "active", all: true, pane: "%3", wantErr: "mutually exclusive"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveScope(c.session, c.all, c.pane)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != c.want {
				t.Fatalf("scope = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestListPanesArgs(t *testing.T) {
	if got := listPanesArgs(scope{all: true}); !slices.Equal(got, []string{"list-panes", "-a", "-F", paneFormat}) {
		t.Errorf("all = %q", got)
	}
	if got := listPanesArgs(scope{target: "=active"}); !slices.Equal(got, []string{"list-panes", "-s", "-t", "=active", "-F", paneFormat}) {
		t.Errorf("session = %q", got)
	}
}

func TestRespawnArgs(t *testing.T) {
	got := respawnArgs("%4", "/p", []string{"/bin/endless", "session", "monitor"})
	want := []string{"respawn-pane", "-k", "-t", "%4", "-c", "/p", "--", "/bin/endless", "session", "monitor"}
	if !slices.Equal(got, want) {
		t.Fatalf("respawnArgs = %q, want %q", got, want)
	}
}

func TestParsePanesDropsUntagged(t *testing.T) {
	out := "%1\ta:1.0\t/x\t\n%2\ta:1.1\t/y z\t10@20\n%3\tb:2.0\t/w\t11@21"
	got := parsePanes(out)
	want := []taggedPane{
		{id: "%2", where: "a:1.1", dir: "/y z", tag: "10@20"},
		{id: "%3", where: "b:2.0", dir: "/w", tag: "11@21"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("parsePanes = %+v, want %+v", got, want)
	}
}

// TestRestartSequence drives restart against a fake tmux: the live monitor is
// respawned with the spawn argv, the stale and unreadable tags are cleared and
// never respawned, and the untagged shell is never touched.
func TestRestartSequence(t *testing.T) {
	list := "%1\ts:1.0\t/a\t\n%2\ts:1.1\t/b\t10@20\n%3\ts:1.2\t/c\t11@21\n%4\ts:1.3\t/d\tjunk"
	var calls [][]string
	defer swap(&tmuxOut, func(args ...string) (string, error) {
		calls = append(calls, args)
		if args[0] == "list-panes" {
			return list, nil
		}
		return "", nil
	})()
	defer swap(&processAlive, func(u upid.UPID) bool { return u.PID == 10 })()
	defer swap(&monitorCommand, func() []string { return []string{"endless", "session", "monitor"} })()

	var w strings.Builder
	if err := restart(&w, scope{target: "=s", label: "s"}, false); err != nil {
		t.Fatalf("restart: %v", err)
	}
	want := [][]string{
		listPanesArgs(scope{target: "=s"}),
		respawnArgs("%2", "/b", []string{"endless", "session", "monitor"}),
		untagArgs("%3"),
		untagArgs("%4"),
	}
	if !slices.EqualFunc(calls, want, slices.Equal) {
		t.Fatalf("tmux calls =\n%q\nwant\n%q", calls, want)
	}
	for _, s := range []string{"restarted %2 (s:1.1)", "skipped %3 (s:1.2): stale tag: process 11 is gone; tag cleared", `skipped %4 (s:1.3): unreadable tag "junk"`} {
		if !strings.Contains(w.String(), s) {
			t.Errorf("summary missing %q:\n%s", s, w.String())
		}
	}
	if strings.Contains(w.String(), "%1") {
		t.Errorf("summary mentions the untagged pane:\n%s", w.String())
	}
}

func TestRestartDryRunChangesNothing(t *testing.T) {
	var calls [][]string
	defer swap(&tmuxOut, func(args ...string) (string, error) {
		calls = append(calls, args)
		return "%2\ts:1.1\t/b\t10@20\n%3\ts:1.2\t/c\t11@21", nil
	})()
	defer swap(&processAlive, func(u upid.UPID) bool { return u.PID == 10 })()

	var w strings.Builder
	if err := restart(&w, scope{all: true, label: "any"}, true); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if len(calls) != 1 || calls[0][0] != "list-panes" {
		t.Fatalf("dry run ran %q, want only list-panes", calls)
	}
	for _, s := range []string{"would restart %2", "would skip %3"} {
		if !strings.Contains(w.String(), s) {
			t.Errorf("summary missing %q:\n%s", s, w.String())
		}
	}
}

func TestRestartNoPanes(t *testing.T) {
	defer swap(&tmuxOut, func(args ...string) (string, error) { return "%1\ts:1.0\t/a\t", nil })()
	var w strings.Builder
	if err := restart(&w, scope{target: "%1", label: "this tmux session"}, false); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if got := w.String(); got != "no session monitor panes in this tmux session\n" {
		t.Fatalf("summary = %q", got)
	}
}

func TestRestartCountsFailures(t *testing.T) {
	defer swap(&tmuxOut, func(args ...string) (string, error) {
		if args[0] == "respawn-pane" {
			return "", errString("boom")
		}
		return "%2\ts:1.1\t/b\t10@20", nil
	})()
	defer swap(&processAlive, func(upid.UPID) bool { return true })()
	var w strings.Builder
	err := restart(&w, scope{all: true}, false)
	if err == nil || !strings.Contains(err.Error(), "1 pane(s) failed") {
		t.Fatalf("err = %v, want a failure count", err)
	}
	if !strings.Contains(w.String(), "failed %2 (s:1.1): boom") {
		t.Fatalf("summary = %q", w.String())
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func swap[T any](p *T, v T) (restore func()) {
	old := *p
	*p = v
	return func() { *p = old }
}
