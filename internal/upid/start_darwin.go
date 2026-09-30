package upid

import (
	"golang.org/x/sys/unix"
)

// startTime reads pid's start time from the kernel's process table, in
// microseconds since the epoch. A pid with no process fails the sysctl.
func startTime(pid int) (start int64, err error) {
	var kp *unix.KinfoProc
	kp, err = unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		goto end
	}
	start = kp.Proc.P_starttime.Sec*1_000_000 + int64(kp.Proc.P_starttime.Usec)
end:
	return start, err
}
