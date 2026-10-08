package mainsyncjob

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/monitor"
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
