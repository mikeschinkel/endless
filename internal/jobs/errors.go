package jobs

import (
	"errors"
)

var (
	// Layer.
	ErrJobs = errors.New("jobs")

	// Categories.
	ErrDatabase    = errors.New("database")
	ErrQuery       = errors.New("query")
	ErrClaiming    = errors.New("claiming job")
	ErrReleasing   = errors.New("releasing job")
	ErrRegistering = errors.New("registering job")

	// Conditions.
	ErrDuplicateJob = errors.New("a job with this name is already registered")
	ErrUnknownJob   = errors.New("no job is registered under this name")
	ErrEmptyName    = errors.New("job name is empty")
	ErrNoInterval   = errors.New("job schedule has a non-positive interval")
	ErrLeaseLost    = errors.New("job lease was taken by another invocation mid-run")
	ErrJobPanicked  = errors.New("job panicked")
)
