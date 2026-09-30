package resumewindowscmd

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseTaskName(t *testing.T) {
	cases := []struct {
		name string
		want int64
		ok   bool
	}{
		{"E-2135", 2135, true},
		{"e-2135", 2135, true},
		{" *E-2187", 2187, true},
		{"*E-2187", 2187, true},
		{"* E-2187 ", 2187, true},
		{"cat", 0, false},
		{"zsh", 0, false},
		{"E-", 0, false},
		{"E-12x", 0, false},
		{"E-2135-fix-thing", 0, false},
		{"ES-1258", 0, false},
		{"**E-1", 0, false},
		{"E-0", 0, false},
	}
	for _, c := range cases {
		got, ok := parseTaskName(c.name)
		if got != c.want || ok != c.ok {
			t.Errorf("parseTaskName(%q) = %d, %v; want %d, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestChoosePane(t *testing.T) {
	wt := "/p/.endless/worktrees/e-7"
	restored := []pane{
		{ID: "%1", Command: "zsh", Path: "/p"},
		{ID: "%2", Command: "zsh", Path: wt + "/internal"},
		{ID: "%3", Command: "zsh", Path: "/p/.endless/worktrees/e-2157"},
	}
	if got, _ := choosePane(restored, wt); got.ID != "%2" {
		t.Errorf("worktree pane not preferred: got %s, want %%2", got.ID)
	}
	if got, _ := choosePane(restored, ""); got.ID != "%1" {
		t.Errorf("no worktree: got %s, want the first shell pane %%1", got.ID)
	}
	// A pane in the worktree that is NOT at a prompt is passed over.
	busy := []pane{
		{ID: "%1", Command: "vim", Path: wt},
		{ID: "%2", Command: "bash", Path: "/p"},
	}
	if got, _ := choosePane(busy, wt); got.ID != "%2" {
		t.Errorf("non-shell worktree pane chosen: got %s, want %%2", got.ID)
	}
	// A sibling directory sharing the worktree's prefix is not inside it.
	sibling := []pane{
		{ID: "%1", Command: "zsh", Path: "/p"},
		{ID: "%2", Command: "zsh", Path: wt + "0"},
	}
	if got, _ := choosePane(sibling, wt); got.ID != "%1" {
		t.Errorf("prefix-sibling treated as the worktree: got %s", got.ID)
	}
	if _, ok := choosePane([]pane{{ID: "%1", Command: "2.1.285"}}, wt); ok {
		t.Error("a pane running something other than a shell was accepted")
	}
}

func restoredWindow() window {
	return window{ID: "@3", Session: "active", Index: "2", Name: " *E-7", Panes: []pane{
		{ID: "%1", Command: "zsh", Path: "/p"},
		{ID: "%2", Command: "zsh", Path: "/p/.endless/worktrees/e-7"},
		{ID: "%3", Command: "zsh", Path: "/p/.endless/worktrees/e-2157"},
	}}
}

func TestDecide_ResumesRestoredWindow(t *testing.T) {
	got := decide(facts{
		Window: restoredWindow(), TaskID: 7, Resolved: true,
		Worktree: "/p/.endless/worktrees/e-7", ProjectRoot: "/p",
	})
	if got.Skip != "" {
		t.Fatalf("skipped: %s", got.Skip)
	}
	if got.Keep != "%2" || !reflect.DeepEqual(got.Kill, []string{"%1", "%3"}) {
		t.Errorf("keep %s kill %v; want keep %%2 kill [%%1 %%3]", got.Keep, got.Kill)
	}
	if want := "cd '/p' && endless session resume E-7 --rebind"; got.Command != want {
		t.Errorf("command = %q, want %q", got.Command, want)
	}
}

func TestDecide_DroppedWorktreeGetsReview(t *testing.T) {
	w := window{Name: "E-9", Panes: []pane{{ID: "%1", Command: "zsh", Path: "/p"}}}
	got := decide(facts{Window: w, TaskID: 9, Resolved: true, ProjectRoot: "/p"})
	if !strings.HasSuffix(got.Command, "endless session resume E-9 --rebind --review") {
		t.Errorf("command = %q, want --review for a reaped worktree", got.Command)
	}
	if len(got.Kill) != 0 {
		t.Errorf("single-pane window killed %v", got.Kill)
	}
}

func TestDecide_UnresolvedStillDispatchesWithWarning(t *testing.T) {
	w := window{Name: "E-9", Panes: []pane{{ID: "%1", Command: "zsh"}}}
	got := decide(facts{Window: w, TaskID: 9, ResolveErr: errors.New("no resumable Claude session found for task E-9")})
	if got.Skip != "" {
		t.Fatalf("skipped: %s — the failure belongs in the window, where the user will look", got.Skip)
	}
	if got.Command != "endless session resume E-9 --rebind" {
		t.Errorf("command = %q (no --review, no cd without a known root)", got.Command)
	}
	if !strings.Contains(got.Warning, "no resumable") {
		t.Errorf("warning = %q, want the resolution error for the summary", got.Warning)
	}
}

func TestDecide_Skips(t *testing.T) {
	w := restoredWindow()
	cases := []struct {
		label string
		f     facts
		want  string
	}{
		{"non-task name", facts{Window: window{Name: "cat"}}, "not named for a task"},
		{"own window", facts{Window: w, TaskID: 7, HoldsSelf: true}, "this command is running in it"},
		{"live session", facts{Window: w, TaskID: 7, LiveSession: 1260}, "already running Claude (ES-1260)"},
		{"live check failed", facts{Window: w, TaskID: 7, LiveErr: errors.New("db")}, "could not check"},
		{"no prompt", facts{Window: window{Name: "E-7", Panes: []pane{{ID: "%1", Command: "2.1.285"}}}, TaskID: 7, Resolved: true}, "no pane is at a shell prompt"},
	}
	for _, c := range cases {
		got := decide(c.f)
		if !strings.Contains(got.Skip, c.want) {
			t.Errorf("%s: skip = %q, want it to contain %q", c.label, got.Skip, c.want)
		}
		if got.Keep != "" || got.Kill != nil || got.Command != "" {
			t.Errorf("%s: a skipped window still has actions: %+v", c.label, got)
		}
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("/a b/it's"); got != `'/a b/it'\''s'` {
		t.Errorf("shellQuote = %s", got)
	}
}

func TestParseWindows_LinkedWindowOnce(t *testing.T) {
	wout := "@1\tactive\t0\tE-1\n@2\tactive\t1\tcat\n@1\tother\t4\tE-1\n"
	pout := "@1\t%1\tzsh\t/p\n@1\t%2\tzsh\t/p\n@2\t%3\tzsh\t/q\n@1\t%1\tzsh\t/p\n@1\t%2\tzsh\t/p\n"
	got := parseWindows(wout, pout)
	if len(got) != 2 {
		t.Fatalf("got %d windows, want 2: %+v", len(got), got)
	}
	if got[0].Session != "active" || len(got[0].Panes) != 2 {
		t.Errorf("linked window: %+v", got[0])
	}
	if got[1].Name != "cat" || len(got[1].Panes) != 1 {
		t.Errorf("second window: %+v", got[1])
	}
}

func TestDispatch_Sequence(t *testing.T) {
	var calls []string
	prev := tmuxOut
	defer func() { tmuxOut = prev }()
	tmuxOut = func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}
	err := dispatch(plan{Keep: "%2", Kill: []string{"%1", "%3"}, Command: "cd '/p' && endless session resume E-7 --rebind"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"kill-pane -t %1",
		"kill-pane -t %3",
		"send-keys -t %2 -X cancel",
		"send-keys -t %2 C-u",
		"send-keys -t %2 -l cd '/p' && endless session resume E-7 --rebind",
		"send-keys -t %2 Enter",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("tmux calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
	for _, c := range calls {
		if strings.Contains(c, "select-window") || strings.Contains(c, "switch-client") {
			t.Errorf("dispatch moved focus: %s", c)
		}
	}
}

func TestRun_ScopeFlags(t *testing.T) {
	var out, errb strings.Builder
	for _, args := range [][]string{
		{},
		{"--tmux-session", "a", "--all-tmux-sessions"},
		{"--all-tmux-sessions", "E-1"},
	} {
		errb.Reset()
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("run(%v) = %d, want 2 (usage)", args, code)
		}
	}
}

// TestInSession_GroupedSessions: `active` and `active-6` are a session group
// sharing every window. list-windows -a lists each window under both, and the
// window must be found by either name — found under neither was the bug.
func TestInSession_GroupedSessions(t *testing.T) {
	wout := "@1\tactive\t1\tE-1\n@2\tactive\t2\tE-2\n@1\tactive-6\t1\tE-1\n@2\tactive-6\t2\tE-2\n@3\tpaused\t0\tcat\n"
	pout := "@1\t%1\tzsh\t/p\n@2\t%2\tzsh\t/p\n@3\t%3\tzsh\t/p\n"
	all := parseWindows(wout, pout)
	if len(all) != 3 {
		t.Fatalf("got %d windows, want 3 (grouped windows once each)", len(all))
	}
	for _, name := range []string{"active", "active-6"} {
		got := inSession(all, name)
		if len(got) != 2 {
			t.Errorf("inSession(%q) = %d windows, want 2", name, len(got))
		}
		for _, w := range got {
			if w.Session != name {
				t.Errorf("inSession(%q) reports window %s under %q", name, w.ID, w.Session)
			}
		}
	}
	if got := inSession(all, "paused"); len(got) != 1 {
		t.Errorf("inSession(paused) = %d, want 1", len(got))
	}
}
