package faults

import (
	"errors"
)

// Sentinels for failures WITHIN the faults package itself. These are distinct
// from the ERR-NNNN catalog in codes.go: the catalog classifies faults that
// callers report, while these describe the recording machinery breaking.
//
// Recording is best-effort by contract (see Record), so most of these are
// swallowed at the boundary rather than propagated. They exist so the read and
// clear paths — which DO return errors to a CLI — can be inspected with
// errors.Is rather than by string matching.
var (
	// Layer.
	ErrFaults = errors.New("faults")

	// Categories.
	ErrDatabase   = errors.New("database")
	ErrNotBound   = errors.New("faults package has no database binding")
	ErrQuery      = errors.New("query")
	ErrScanning   = errors.New("scanning row")
	ErrRecording  = errors.New("recording fault")
	ErrClearing   = errors.New("clearing fault")
	ErrReadingLog = errors.New("reading detail log")

	// Catalog integrity.
	ErrUnknownCode   = errors.New("unknown error code")
	ErrDuplicateCode = errors.New("duplicate error code in catalog")
	ErrEmptySummary  = errors.New("fault summary is empty")
)
