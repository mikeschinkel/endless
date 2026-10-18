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
	"strings"

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
// is linked, unlinked, renamed, moved or swapped, and only when the windows or
// their order actually changed (E-2385: restore brings them back in tab
// order, so the order is part of what is recorded). It
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
	// Windows is the task ids with a window in the tasks session, in tab
	// (window index) order.
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
// Order is part of the record, so a reorder alone is a difference. Reports
// whether it wrote.
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

// normalizeIDs drops repeats, keeping each id's first position: two windows
// for one task restore as one, where the first of them stood.
func normalizeIDs(ids []int64) []int64 {
	out := []int64{}
	seen := map[int64]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
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

// TaskWindows lists the task ids with a window in the server's session, in
// tab order. ok is
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
	wins, ok := s.Windows()
	if !ok {
		return nil, false
	}
	for _, w := range wins {
		if w.TaskID > 0 {
			ids = append(ids, w.TaskID)
		}
	}
	return normalizeIDs(ids), true
}

// Window is one window of the server's session.
type Window struct {
	ID     string // @N
	Index  int
	Name   string
	TaskID int64 // 0 when the name is not a task's
}

// Windows lists the session's windows in tab order. ok is false when the
// server could not be asked.
func (s Server) Windows() (wins []Window, ok bool) {
	out, err := s.Out("list-windows", "-t", "="+s.Session+":", "-F", "#{window_index}\t#{window_id}\t#{window_name}")
	if err != nil {
		return nil, false
	}
	return parseWindows(out), true
}

// parseWindows reads `list-windows -F "#{window_index}\t#{window_id}\t#{window_name}"`
// output, sorted by index: tmux lists in index order already, and sorting
// makes that a property of this function rather than of tmux. Pure.
func parseWindows(out string) []Window {
	var wins []Window
	for _, line := range splitLines(out) {
		idx, rest, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		id, name, _ := strings.Cut(rest, "\t")
		n, err := strconv.Atoi(strings.TrimSpace(idx))
		if err != nil {
			continue
		}
		w := Window{ID: id, Index: n, Name: name}
		w.TaskID, _ = TaskIDOfWindow(name)
		wins = append(wins, w)
	}
	slices.SortStableFunc(wins, func(a, b Window) int { return a.Index - b.Index })
	return wins
}

// StoppingOption is the server option `endless stop tasks` sets while it
// closes the server's windows (E-2385). While it is set, Record writes
// nothing, so the record keeps the windows that were open when the stop
// began rather than following them shut one by one.
const StoppingOption = "@endless_stopping"

// Stopping reports whether a stop has frozen the record of this server.
func (s Server) Stopping() bool {
	v, err := s.Out("show-options", "-gqv", StoppingOption)
	return err == nil && v != ""
}

// Record re-observes the server's task windows and rewrites the record when
// they or their order changed. A server that cannot be asked leaves the record
// alone: it is the record of what was open when the server was last seen. So
// does a server being stopped (see StoppingOption).
func (s Server) Record() (wrote bool, err error) {
	if s.Stopping() {
		return false, nil
	}
	ids, ok := s.TaskWindows()
	if !ok {
		return false, nil
	}
	return WriteState(ids)
}

func splitLines(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
}
