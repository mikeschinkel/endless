package tmuxservers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"

	"github.com/mikeschinkel/endless/internal/config"
)

// The record of which task windows are open on the task-session server.
//
// tmux itself always knows which windows are open now; this file exists only
// to know which WERE open, after the server is gone — killed, crashed, or lost
// to a reboot — so `endless tmux start` can bring exactly those back. A window
// closed before the server went away is not in it, so it does not come back.
//
// It is rewritten by hooks on the task-session server (conf.go) when a window
// is linked, unlinked or renamed, and only when the set actually changed. It
// lives in the user's Endless config directory rather than beside the socket,
// because /tmp does not survive the reboot it has to outlive. It is
// machine-local runtime state, so it is not a ledger event.

// StateFile is where the record lives.
func StateFile() (string, error) {
	f, err := config.MachineFile()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(string(f)), "tmux-task-windows.json"), nil
}

type stateDoc struct {
	// Windows is the task ids, ascending, with a window in the tasks session.
	Windows []int64 `json:"windows"`
}

// ReadState returns the recorded task ids. A missing file is an empty record.
func ReadState() ([]int64, error) {
	path, err := StateFile()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc stateDoc
	if err = json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return doc.Windows, nil
}

// WriteState replaces the record when it differs from ids, atomically.
// Reports whether it wrote.
func WriteState(ids []int64) (bool, error) {
	ids = normalizeIDs(ids)
	prev, err := ReadState()
	if err == nil && slices.Equal(normalizeIDs(prev), ids) {
		return false, nil
	}
	path, err := StateFile()
	if err != nil {
		return false, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	data, err := json.Marshal(stateDoc{Windows: ids})
	if err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmux-task-windows-*")
	if err != nil {
		return false, err
	}
	_, err = tmp.Write(append(data, '\n'))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return false, err
	}
	return true, nil
}

func normalizeIDs(ids []int64) []int64 {
	out := slices.Clone(ids)
	if out == nil {
		out = []int64{}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// taskWindowName matches a window named for a task: `E-NNNN`, optionally
// preceded by the attention marker `*` — the same rule the resume sweep reads
// window names by.
var taskWindowName = regexp.MustCompile(`^\s*(?:\*\s*)?[Ee]-(\d+)\s*$`)

// TaskIDOfWindow returns the task id a window name carries.
func TaskIDOfWindow(name string) (int64, bool) {
	m := taskWindowName.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// TaskWindows lists the task ids with a window in the server's session. ok is
// false when the server could not be asked — no record should be written
// from an observation that never happened. A server that answers but no
// longer has the session (its last window just closed) has no task windows.
func (s Server) TaskWindows() (ids []int64, ok bool) {
	if !s.Reachable() {
		return nil, false
	}
	if !s.HasSession() {
		return []int64{}, true
	}
	out, err := s.Out("list-windows", "-t", "="+s.Session+":", "-F", "#{window_name}")
	if err != nil {
		return nil, false
	}
	for _, name := range splitLines(out) {
		if id, isTask := TaskIDOfWindow(name); isTask {
			ids = append(ids, id)
		}
	}
	return normalizeIDs(ids), true
}

// Record re-observes the server's task windows and rewrites the record when
// the set changed. A server that cannot be asked leaves the record alone: it
// is the record of what was open when the server was last seen.
func (s Server) Record() (wrote bool, err error) {
	ids, ok := s.TaskWindows()
	if !ok {
		return false, nil
	}
	return WriteState(ids)
}

func splitLines(s string) []string {
	var out []string
	for _, l := range regexp.MustCompile(`\r?\n`).Split(s, -1) {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
