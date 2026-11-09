package mainsyncjob

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/outages"
)

// TestUnreachable_MarksOnlyNetworkFailures: each phrase git or ssh prints when
// the remote could not be reached is transient; a rejected push and a failed
// authentication are not, so they still report at once (E-2260).
func TestUnreachable_MarksOnlyNetworkFailures(t *testing.T) {
	for _, tc := range []struct {
		stderr    string
		transient bool
	}{
		{"ssh: Could not resolve hostname github.com: nodename nor servname provided, or not known", true},
		{"fatal: unable to access 'https://github.com/x/y.git/': Could not resolve host: github.com", true},
		{"ssh: connect to host github.com port 22: Connection refused", true},
		{"ssh: connect to host github.com port 22: Network is unreachable", true},
		{"ssh: connect to host github.com port 22: Operation timed out", true},
		{"ssh: connect to host github.com port 22: Connection timed out", true},
		{"! [remote rejected] main -> main (pre-receive hook declined)", false},
		{"git@github.com: Permission denied (publickey).", false},
		{"fatal: Authentication failed for 'https://github.com/x/y.git/'", false},
	} {
		err := unreachable(fmt.Errorf("fetch origin: %w",
			fmt.Errorf("git fetch --quiet origin: exit status 128: %s", tc.stderr)))
		if got := jobs.IsTransient(err); got != tc.transient {
			t.Errorf("IsTransient(%q) = %v, want %v", tc.stderr, got, tc.transient)
		}
	}
}

// failingFetch is real git except that a fetch fails with stderr.
func failingFetch(stderr string) Git {
	return func(ctx context.Context, dir string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "fetch" {
			return "", fmt.Errorf("git %s: exit status 128: %s", strings.Join(args, " "), stderr)
		}
		return systemGit(ctx, dir, args...)
	}
}

// TestRunProjects_TransientOnlyWhenEveryFailureIs: an unreachable remote makes
// the run's error transient, and one project failing any other way makes it
// not, so that failure is not held back behind another project's outage.
func TestRunProjects_TransientOnlyWhenEveryFailureIs(t *testing.T) {
	optIn := func(root string) bool { return true }
	a, b := newFixture(t), newFixture(t)

	_, err := runProjects(context.Background(),
		[]monitor.ProjectRef{{ID: 1, Root: a.main}}, optIn,
		failingFetch("ssh: Could not resolve hostname github.com"))
	if err == nil || !jobs.IsTransient(err) {
		t.Errorf("unreachable remote: err = %v, transient = %v; want transient", err, jobs.IsTransient(err))
	}

	b.breakUpstream(t)
	_, err = runProjects(context.Background(),
		[]monitor.ProjectRef{{ID: 1, Root: a.main}, {ID: 2, Root: b.main}}, optIn,
		failingFetch("ssh: Could not resolve hostname github.com"))
	if err == nil || jobs.IsTransient(err) {
		t.Errorf("mixed failures: err = %v, transient = %v; want not transient", err, jobs.IsTransient(err))
	}
	if err == nil || !strings.Contains(err.Error(), "no remote upstream") {
		t.Errorf("err = %v, want the non-transient failure named", err)
	}
}

// breakUpstream removes main's upstream, a failure no retry fixes.
func (f fixture) breakUpstream(t *testing.T) {
	t.Helper()
	git(t, f.main, "branch", "--unset-upstream")
}

func TestIsGitHub(t *testing.T) {
	for url, want := range map[string]bool{
		"git@github.com:mikeschinkel/endless.git": true,
		"ssh://git@ssh.github.com:443/o/r.git":    true,
		"https://github.com/o/r.git":              true,
		"https://user:token@github.com/o/r":       true,
		"git://github.com/o/r":                    true,
		"git@gitlab.com:o/r.git":                  false,
		"https://github.com.evil.example/o/r":     false,
		"/Users/someone/repos/origin.git":         false,
		"":                                        false,
	} {
		if got := isGitHub(url); got != want {
			t.Errorf("isGitHub(%q) = %v, want %v", url, got, want)
		}
	}
}

// githubFetch fails every fetch with stderr and answers `remote get-url` with
// a github.com URL; everything else is real git.
func githubFetch(stderr string) Git {
	return func(ctx context.Context, dir string, args ...string) (string, error) {
		switch {
		case len(args) > 1 && args[0] == "remote" && args[1] == "get-url":
			return "git@github.com:o/r.git\n", nil
		case len(args) > 0 && args[0] == "fetch":
			return "", fmt.Errorf("git %s: exit status 128: %s", strings.Join(args, " "), stderr)
		}
		return systemGit(ctx, dir, args...)
	}
}

// stubConfirm makes GitHub's status page answer confirmed, and counts asks.
func stubConfirm(t *testing.T, confirmed bool) *int {
	t.Helper()
	var asks int
	prev := confirmOutage
	confirmOutage = func(context.Context, outages.Provider, string) bool {
		asks++
		return confirmed
	}
	t.Cleanup(func() { confirmOutage = prev })
	return &asks
}

// P3: during a confirmed GitHub outage a failed fetch is an outage, an
// authentication failure included.
func TestRunProjects_GitHubOutageExcusesTheFailure(t *testing.T) {
	a := newFixture(t)
	asks := stubConfirm(t, true)

	_, err := runProjects(context.Background(),
		[]monitor.ProjectRef{{ID: 1, Root: a.main}}, func(string) bool { return true },
		githubFetch("git@github.com: Permission denied (publickey)."))
	if err == nil || !jobs.IsOutage(err) || jobs.IsTransient(err) {
		t.Fatalf("err = %v, outage = %v; want an outage", err, jobs.IsOutage(err))
	}
	if *asks != 1 {
		t.Errorf("status page asked %d times, want 1", *asks)
	}
}

// P5: with no confirmed incident the failure is handled as before — an
// authentication failure stays a plain failure, an unreachable remote stays
// transient.
func TestRunProjects_NoIncidentHandlesTheFailureAsBefore(t *testing.T) {
	a := newFixture(t)
	stubConfirm(t, false)
	optIn := func(string) bool { return true }

	_, err := runProjects(context.Background(), []monitor.ProjectRef{{ID: 1, Root: a.main}}, optIn,
		githubFetch("git@github.com: Permission denied (publickey)."))
	if err == nil || jobs.IsOutage(err) || jobs.IsTransient(err) {
		t.Errorf("auth failure: err = %v; want a plain failure", err)
	}

	_, err = runProjects(context.Background(), []monitor.ProjectRef{{ID: 1, Root: a.main}}, optIn,
		githubFetch("ssh: Could not resolve hostname github.com"))
	if err == nil || jobs.IsOutage(err) || !jobs.IsTransient(err) {
		t.Errorf("unreachable: err = %v; want transient", err)
	}
}

// A remote that is not on github.com never asks GitHub.
func TestRemoteFailure_OtherHostsDoNotAsk(t *testing.T) {
	a := newFixture(t)
	asks := stubConfirm(t, true)

	_, err := runProjects(context.Background(),
		[]monitor.ProjectRef{{ID: 1, Root: a.main}}, func(string) bool { return true },
		failingFetch("ssh: Could not resolve hostname example.org"))
	if *asks != 0 {
		t.Errorf("asked GitHub %d times for a local remote", *asks)
	}
	if !jobs.IsTransient(err) {
		t.Errorf("err = %v, want transient", err)
	}
}
