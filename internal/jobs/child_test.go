package jobs

import (
	"os"
	"strings"
	"testing"
)

// TestChildEnvPinsConfigHome covers the wiring detail that would otherwise
// silently point a job's subprocess at the WRONG database: the Python CLI
// takes no directory, so XDG_CONFIG_HOME is the entire mechanism for telling the
// subprocess which database to open.
func TestChildEnvPinsConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/e1975-parent")

	var seen []string
	for _, kv := range ChildEnv() {
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
	if len(ChildEnv()) < len(os.Environ()) {
		t.Error("ChildEnv dropped variables beyond XDG_CONFIG_HOME")
	}
}
