// Package upid defines a UPID — a unique process identifier: a pid plus that
// process's start time.
//
// A bare pid is not an identity. The OS reissues it the moment its process
// exits, so a pid written down earlier can name an unrelated process now. The
// OS never gives one pid to two processes started at the same instant, so the
// pair cannot be reused: a UPID either names the process it was taken from or
// names nothing.
//
// The start time is opaque. Each platform reports it in its own unit (see
// start_*.go), and a UPID is only ever compared with another taken on the same
// machine, so nothing converts it to a wall clock.
package upid

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// UPID is a pid plus that process's start time.
type UPID struct {
	PID   int
	Start int64
}

// sep separates the pid from the start time in String/Parse.
const sep = "@"

// String renders the UPID as `<pid>@<start>`, the form Parse reads back.
func (u UPID) String() string {
	return strconv.Itoa(u.PID) + sep + strconv.FormatInt(u.Start, 10)
}

// Parse reads a UPID in the form String writes.
func Parse(s string) (u UPID, err error) {
	pid, start, ok := strings.Cut(strings.TrimSpace(s), sep)
	if !ok {
		err = fmt.Errorf("upid %q: want <pid>%s<start>", s, sep)
		goto end
	}
	u.PID, err = strconv.Atoi(pid)
	if err != nil || u.PID <= 0 {
		err = fmt.Errorf("upid %q: bad pid", s)
		goto end
	}
	u.Start, err = strconv.ParseInt(start, 10, 64)
	if err != nil {
		err = fmt.Errorf("upid %q: bad start time", s)
		goto end
	}
end:
	return u, err
}

// Of returns the UPID of the running process pid. It fails when no such
// process exists, or on a platform with no start-time source.
func Of(pid int) (u UPID, err error) {
	var start int64
	start, err = startTime(pid)
	if err != nil {
		goto end
	}
	u = UPID{PID: pid, Start: start}
end:
	return u, err
}

// Self returns this process's UPID.
func Self() (UPID, error) {
	return Of(os.Getpid())
}

// Alive reports whether the process u names is still running: a process with
// u's pid exists AND it started when u says. The same pid with a different
// start time is a different process, and reports false.
func (u UPID) Alive() bool {
	now, err := Of(u.PID)
	return err == nil && now == u
}
