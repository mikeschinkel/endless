package verifycmd

import (
	"errors"
)

var (
	ErrResolvingRoot       = errors.New("resolving current directory")
	ErrProjectRootNotFound = errors.New("no project root (.endless directory) found walking up from cwd")
	ErrMakingRunDir        = errors.New("creating per-run temp directory")
	ErrIsolatingEnv        = errors.New("preparing isolated HOME/XDG_CONFIG_HOME")
	ErrNoSuiteForTask      = errors.New("no verification suite found for task")

	// Tier-0 boundary: escalation beyond a temp working dir + isolated env is
	// a later stage; seeding is E-1606. Both fail loudly rather than run
	// something weaker than the manifest asked for.
	ErrTierNotSupported = errors.New("suite declares needs; only Tier 0 (temp dir + isolated env) is supported")
	ErrSeedNotSupported = errors.New("suite declares seed; seeding is not yet supported")

	// Precondition and check execution. A driver owns its own start /
	// no-stream / normalize errors (see internal/verify); this guard is the
	// runner's exit-semantics interpretation across a driver's result.
	ErrSetupStep            = errors.New("setup step failed")
	ErrCheckFailedNoResults = errors.New("check exited non-zero but reported no test failures")

	// Reporting.
	ErrWritingCTRF = errors.New("writing merged CTRF report")
)
