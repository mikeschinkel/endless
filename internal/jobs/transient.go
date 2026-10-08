package jobs

import (
	"errors"
)

// TransientThreshold is how many consecutive transient failures a job may have
// before the runner says so (E-2260). Below it a transient failure records
// nothing: the job's own backoff is already retrying it, and the error list is
// for problems that need a person. With main-sync's backoff (5m, 10m, 20m,
// 40m) four failures is about seventy-five minutes of outage.
const TransientThreshold = 4

// transientError marks a failure its job expects to pass on retry.
type transientError struct {
	err error
}

func (e transientError) Error() string { return e.err.Error() }
func (e transientError) Unwrap() error { return e.err }

// Transient marks err as a failure likely to pass on retry — a DNS failure, a
// refused connection, a timeout. The runner does not classify errors itself; a
// job marks the failures it knows are transient, and the runner then reports
// them only after TransientThreshold of them in a row. errors.Is and errors.As
// still reach the cause. Transient(nil) is nil.
func Transient(err error) (marked error) {
	if err != nil {
		marked = transientError{err: err}
	}
	return marked
}

// IsTransient reports whether err, or anything it wraps, was marked Transient.
func IsTransient(err error) (transient bool) {
	var te transientError
	transient = errors.As(err, &te)
	return transient
}
