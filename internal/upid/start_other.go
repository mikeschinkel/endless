//go:build !darwin && !linux

package upid

import (
	"fmt"
	"runtime"
)

// startTime has no source on this platform, so no UPID can be taken and none
// is ever reported alive. Callers treat that as "cannot tell", which is the
// safe answer for anything that would act on a process.
func startTime(pid int) (int64, error) {
	return 0, fmt.Errorf("process start time: unsupported on %s", runtime.GOOS)
}
