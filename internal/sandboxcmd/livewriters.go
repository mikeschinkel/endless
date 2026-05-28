package sandboxcmd

import (
	"bufio"
	"bytes"
	"os/exec"
	"strconv"
)

// liveWriter is a process holding files open under a sandbox dir.
type liveWriter struct {
	PID  int
	Name string
}

// findLiveWriters returns processes whose CWD or any open FD is inside dir.
// Uses lsof; returns nil if lsof isn't available so destroy can still proceed
// best-effort on systems without it.
func findLiveWriters(dir string) []liveWriter {
	if _, err := exec.LookPath("lsof"); err != nil {
		return nil
	}
	// -F selects field-mode output (one prefixed line per attribute).
	// p=pid, c=command-name. +D recurses dir matching cwd and open files.
	//
	// lsof's exit status is unreliable here: it returns 1 both when no
	// matches are found AND when partial scan errors occurred (which is
	// common with +D walking a tree containing inaccessible entries).
	// Stdout is the source of truth — empty means no writers, non-empty
	// means parse it.
	cmd := exec.Command("lsof", "-Fpc", "+D", dir)
	out, _ := cmd.Output()
	return parseLsofPidCommandOutput(out)
}

// parseLsofPidCommandOutput parses `lsof -Fpc` output into one entry per PID.
// lsof emits a 'p<PID>' line followed by a 'c<command>' line, then per-fd
// lines we ignore. We only need the unique (pid, name) pairs.
func parseLsofPidCommandOutput(out []byte) []liveWriter {
	seen := map[int]string{}
	order := []int{}
	var curPID int
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, err := strconv.Atoi(line[1:])
			if err != nil {
				continue
			}
			curPID = pid
			if _, ok := seen[curPID]; !ok {
				seen[curPID] = ""
				order = append(order, curPID)
			}
		case 'c':
			if curPID != 0 {
				seen[curPID] = line[1:]
			}
		}
	}
	writers := make([]liveWriter, 0, len(order))
	for _, pid := range order {
		writers = append(writers, liveWriter{PID: pid, Name: seen[pid]})
	}
	return writers
}

