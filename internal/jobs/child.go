package jobs

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// ChildEnv is the parent environment with XDG_CONFIG_HOME repointed at the
// runner's RESOLVED config directory, so a job's `endless` subprocess opens the
// database this process is using rather than whatever the ambient environment
// names.
//
// The Python CLI takes --db main|sandbox, not a directory — there is no way to
// hand it one — so the environment is the whole mechanism. Endless's config
// dir is always <XDG_CONFIG_HOME>/endless, so handing the child the PARENT of
// ConfigDir() reproduces the resolution exactly, sandbox included. Without
// this, a self-dev run would act on the wrong database.
//
// Shared here because more than one job shells the Python CLI (the minimizer
// loop and auto-spawn); the triage job that first wrote it is gone (E-1993).
func ChildEnv() (env []string) {
	const key = "XDG_CONFIG_HOME"
	value := filepath.Dir(monitor.ConfigDir())

	env = make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, key+"=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, key+"="+value)
}
