package triagejob

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/jobs"
)

// TestRegistered pins the wiring, not the logic: the job's init() must have put
// it in the registry. A job that compiles but never registers is silently dead,
// and `jobs list` would report "no jobs registered" with no other symptom.
func TestRegistered(t *testing.T) {
	got, ok := jobs.Lookup(JobName)
	if !ok {
		t.Fatalf("%q is not registered; the runner will never fire it", JobName)
	}
	if got.Name() != JobName {
		t.Errorf("Name() = %q, want %q", got.Name(), JobName)
	}
}

// TestScheduleLeaseExceedsWorstCase is the lease contract from
// internal/jobs: once the lease expires another invocation may claim and run
// the job CONCURRENTLY. The worst case here is batchLimit sequential model
// calls, each bounded by perTaskTimeout, so the lease must exceed that product
// — otherwise a healthy but slow sweep gets re-claimed underneath itself.
func TestScheduleLeaseExceedsWorstCase(t *testing.T) {
	s := job{}.Schedule()
	worst := time.Duration(batchLimit) * perTaskTimeout
	if s.LeaseTTL <= worst {
		t.Errorf("LeaseTTL %s does not exceed worst-case sweep %s", s.LeaseTTL, worst)
	}
	if s.Interval <= 0 {
		t.Error("Interval must be positive; the zero Schedule is invalid")
	}
	// MaxBackoff is not optional for this job. internal/jobs names
	// model-calling jobs as exactly the case it exists for: without it a
	// persistently broken triager burns model spend every Interval forever.
	if s.MaxBackoff <= s.Interval {
		t.Errorf("MaxBackoff %s must exceed Interval %s to actually back off",
			s.MaxBackoff, s.Interval)
	}
}

// TestChildEnvPinsConfigHome covers the wiring detail that would otherwise
// silently triage the WRONG database: the Python CLI has no --config-dir, so
// XDG_CONFIG_HOME is the entire mechanism for telling the subprocess which
// database to open.
func TestChildEnvPinsConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/e1859-parent")

	var seen []string
	for _, kv := range childEnv() {
		if strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			seen = append(seen, kv)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("XDG_CONFIG_HOME appears %d times in the child env: %v", len(seen), seen)
	}
	// monitor.ConfigDir() appends "endless" to XDG_CONFIG_HOME, and childEnv
	// hands the child the parent of that — so the round trip must land back on
	// the value set above rather than on some other directory.
	if seen[0] != "XDG_CONFIG_HOME=/tmp/e1859-parent" {
		t.Errorf("child env: got %q, want XDG_CONFIG_HOME=/tmp/e1859-parent", seen[0])
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
	if tail("  hi  ") != "hi" {
		t.Errorf("tail did not trim: %q", tail("  hi  "))
	}
}
