package jobs

import (
	"errors"
)

// TransientThreshold is how many consecutive transient failures a job may have
// before the runner says so (E-2260). Below it a transient failure records
// nothing: the job's own backoff is already retrying it, and the error list is
// for problems that need a person. With main-sync's backoff (5m, 10m, 20m,
// 40m) four failures is about seventy-five minutes of outage.
//
// The count is the job's transient run in the faults store
// (faults.RecordTransient), not the scheduling row's fail_count, so a job's
// runs sit in one history with every other source's (E-2589).
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

// outageError marks a failure whose cause a provider's status page confirmed.
type outageError struct {
	err error
}

func (e outageError) Error() string { return e.err.Error() }
func (e outageError) Unwrap() error { return e.err }

// Outage marks err as a failure a provider's status page has confirmed, for
// which the job has opened an outage record (E-2589). The runner records the
// failure on the scheduling row and backs off as for any other, but raises no
// fault and counts no transient occurrence: the open outage is what the user
// sees, and it closes itself. Outage(nil) is nil.
func Outage(err error) (marked error) {
	if err != nil {
		marked = outageError{err: err}
	}
	return marked
}

// IsOutage reports whether err, or anything it wraps, was marked Outage.
func IsOutage(err error) (outage bool) {
	var oe outageError
	outage = errors.As(err, &oe)
	return outage
}
