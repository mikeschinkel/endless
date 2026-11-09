package dbrecover

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Holder is a process with the database open.
type Holder struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
}

// ErrHoldersUnknown means neither lsof nor /proc could say who has the file
// open. It is not "nobody": a caller must treat it as the unsafe answer.
var ErrHoldersUnknown = errors.New("cannot determine what has the database open: no lsof and no /proc")

// FileSet is the database and every sidecar SQLite may keep beside it.
func FileSet(dbPath string) []string {
	set := []string{dbPath}
	for _, suffix := range SidecarSuffixes {
		set = append(set, dbPath+suffix)
	}
	return set
}

// Holders lists the processes other than this one holding any of paths open.
// It mirrors the Python `db restore` report: lsof where it exists, /proc where
// it does not, and ErrHoldersUnknown when neither can be asked.
func Holders(ctx context.Context, paths []string) (holders []Holder, err error) {
	var existing []string
	for _, p := range paths {
		if _, statErr := os.Stat(p); statErr == nil {
			existing = append(existing, p)
		}
	}
	if len(existing) == 0 {
		return nil, nil
	}
	pids, ok := pidsViaLsof(ctx, existing)
	if !ok {
		pids, ok = pidsViaProc(existing)
	}
	if !ok {
		return nil, ErrHoldersUnknown
	}
	seen := map[int]bool{}
	for _, pid := range pids {
		if pid == os.Getpid() || seen[pid] {
			continue
		}
		seen[pid] = true
		holders = append(holders, Holder{PID: pid, Command: commandOf(ctx, pid)})
	}
	sort.Slice(holders, func(i, j int) bool { return holders[i].PID < holders[j].PID })
	return holders, nil
}

// WaitForHolders polls until nothing but this process holds paths open, or
// wait runs out, and returns whoever still holds them. A process that took the
// maintenance lock calls it: every Endless opener lets go on its next connect,
// so most holders leave within one tick of their own loop.
func WaitForHolders(ctx context.Context, paths []string, wait, poll time.Duration) ([]Holder, error) {
	deadline := time.Now().Add(wait)
	for {
		holders, err := Holders(ctx, paths)
		if err != nil || len(holders) == 0 || !time.Now().Before(deadline) {
			return holders, err
		}
		select {
		case <-ctx.Done():
			return holders, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// pidsViaLsof asks lsof. Exit status 1 is lsof saying some named file is
// not open — with several files named that is the normal answer, and the pids
// it did find are still on stdout — so only a failure to run counts.
func pidsViaLsof(ctx context.Context, paths []string) (pids []int, ok bool) {
	bin, err := exec.LookPath("lsof")
	if err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, append([]string{"-t", "--"}, paths...)...).Output()
	var exitErr *exec.ExitError
	if err != nil && !(errors.As(err, &exitErr) && exitErr.ExitCode() == 1) {
		return nil, false
	}
	for _, field := range strings.Fields(string(out)) {
		if pid, convErr := strconv.Atoi(field); convErr == nil {
			pids = append(pids, pid)
		}
	}
	return pids, true
}

// pidsViaProc walks /proc/*/fd, for a Linux machine without lsof.
func pidsViaProc(paths []string) (pids []int, ok bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, false
	}
	targets := map[string]bool{}
	for _, p := range paths {
		if real, realErr := filepath.EvalSymlinks(p); realErr == nil {
			targets[real] = true
		}
	}
	for _, entry := range entries {
		pid, convErr := strconv.Atoi(entry.Name())
		if convErr != nil {
			continue
		}
		fdDir := filepath.Join("/proc", entry.Name(), "fd")
		fds, readErr := os.ReadDir(fdDir)
		if readErr != nil {
			continue
		}
		for _, fd := range fds {
			if real, linkErr := os.Readlink(filepath.Join(fdDir, fd.Name())); linkErr == nil && targets[real] {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids, true
}

// commandOf is pid's full command line, or "?".
func commandOf(ctx context.Context, pid int) string {
	out, err := exec.CommandContext(ctx, "ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "?"
	}
	if cmd := strings.TrimSpace(string(out)); cmd != "" {
		return cmd
	}
	return "?"
}
