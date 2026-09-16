package minimizerjob

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/jobs"
)

// TestRegistered pins the wiring, not the logic: the job's init() must have put
// it in the registry. A job that compiles but never registers is silently dead,
// and the loop would simply never run while every other symptom looked fine.
func TestRegistered(t *testing.T) {
	got, ok := jobs.Lookup(JobName)
	if !ok {
		t.Fatalf("%q is not registered; the runner will never fire it", JobName)
	}
	if got.Name() != JobName {
		t.Errorf("Name() = %q, want %q", got.Name(), JobName)
	}
}

// TestScheduleLeaseExceedsWorstCase is the lease contract from internal/jobs:
// once the lease expires another invocation may claim and run the job
// CONCURRENTLY. The worst case here is a full judge sweep AND an optimize round
// landing on the same tick, so the lease must exceed that sum.
func TestScheduleLeaseExceedsWorstCase(t *testing.T) {
	s := job{}.Schedule()
	worst := time.Duration(judgeLimit+replayCalls) * perCallTimeout
	if s.LeaseTTL <= worst {
		t.Errorf("LeaseTTL %s does not exceed worst-case tick %s", s.LeaseTTL, worst)
	}
	if s.Interval <= 0 {
		t.Error("Interval must be positive; the zero Schedule is invalid")
	}
	// MaxBackoff is not optional for this job. internal/jobs names model-calling
	// jobs as exactly the case it exists for: without it a persistently broken
	// loop burns model spend every Interval forever.
	if s.MaxBackoff <= s.Interval {
		t.Errorf("MaxBackoff %s must exceed Interval %s to actually back off",
			s.MaxBackoff, s.Interval)
	}
}

// TestIntervalIsPromptEnoughToStayBlind is the one scheduling property that is
// about correctness rather than cost.
//
// A judgment is only a PREDICTION while the user has not reacted. A sweep that
// ran hourly would routinely judge turns the user had already answered, and
// every one of those is excluded from the calibration number — so a slow tick
// does not merely delay the loop's honesty check, it starves it.
func TestIntervalIsPromptEnoughToStayBlind(t *testing.T) {
	got := job{}.Schedule().Interval
	if got > 15*time.Minute {
		t.Errorf("Interval %s is too slow to judge turns before the user reacts", got)
	}
}

// TestChildEnvPinsConfigHome covers the wiring detail that would otherwise
// silently tune the WRONG database's minimizer: the Python CLI has no
// a directory, so XDG_CONFIG_HOME is the entire mechanism for telling the
// subprocess which database to open.
func TestChildEnvPinsConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/e1975-parent")

	var seen []string
	for _, kv := range childEnv() {
		if strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			seen = append(seen, kv)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("XDG_CONFIG_HOME appears %d times in the child env: %v", len(seen), seen)
	}
	if seen[0] != "XDG_CONFIG_HOME=/tmp/e1975-parent" {
		t.Errorf("child env: got %q, want XDG_CONFIG_HOME=/tmp/e1975-parent", seen[0])
	}
	// The rest of the parent environment must survive: `claude` needs PATH and
	// its OAuth/keychain access, and a bare env would strip both.
	if len(childEnv()) < len(os.Environ()) {
		t.Error("childEnv dropped variables beyond XDG_CONFIG_HOME")
	}
}

func TestTailBoundsFaultDetail(t *testing.T) {
	long := strings.Repeat("x", tailLimit*2)
	got := tail(long)
	if len(got) > tailLimit+len("…") {
		t.Errorf("tail returned %d bytes, want <= %d", len(got), tailLimit+len("…"))
	}
	if got := tail("  short  "); got != "short" {
		t.Errorf("tail(%q) = %q, want trimmed", "  short  ", got)
	}
}
