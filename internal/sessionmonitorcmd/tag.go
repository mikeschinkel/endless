// Package sessionmonitorcmd owns what Endless knows about the panes a `session
// monitor` runs in (E-2194): the monitor tags its own pane when it starts, and
// `endless-go session-monitor restart` finds tagged panes and respawns each in
// place onto the installed binary.
//
// A monitor pane is not a session, so nothing in the database records it. The
// tag is that record, and it lives on the pane itself, where tmux drops it when
// the pane goes away.
package sessionmonitorcmd

import (
	"os/exec"
	"strings"

	"github.com/mikeschinkel/endless/internal/upid"
)

// TagOption is the PANE option a session monitor sets on its own pane. Its
// value is the monitor process's UPID — the only thing in the tag that
// identifies the monitor, and the only thing a restart trusts.
const TagOption = "@endless_session_monitor"

// tagArgs builds `tmux set-option -p -t <pane> @endless_session_monitor <value>`.
func tagArgs(pane, value string) []string {
	return []string{"set-option", "-p", "-t", pane, TagOption, value}
}

// untagArgs builds `tmux set-option -p -u -t <pane> @endless_session_monitor`.
func untagArgs(pane string) []string {
	return []string{"set-option", "-p", "-u", "-t", pane, TagOption}
}

// readTagArgs builds `tmux show-options -p -v -t <pane> @endless_session_monitor`.
func readTagArgs(pane string) []string {
	return []string{"show-options", "-p", "-v", "-t", pane, TagOption}
}

// Tag marks pane as holding this process's session monitor, and returns the
// func that removes the mark on a clean exit.
//
// Best-effort throughout: a monitor that cannot tag its pane is still a
// monitor, it just cannot be found by `--restart`. With no pane (outside tmux)
// or no UPID (no start-time source on this platform) there is nothing to write,
// and the returned func does nothing.
//
// The untag clears the option only while it still holds THIS process's UPID. A
// respawn can start the replacement monitor before the old one has finished
// dying; an unconditional clear from the old process would erase the tag the
// new one just wrote.
func Tag(pane string) (untag func()) {
	untag = func() {}
	if pane == "" {
		return untag
	}
	self, err := upid.Self()
	if err != nil {
		return untag
	}
	value := self.String()
	if exec.Command("tmux", tagArgs(pane, value)...).Run() != nil {
		return untag
	}
	return func() {
		out, err := exec.Command("tmux", readTagArgs(pane)...).Output()
		if err != nil || strings.TrimSpace(string(out)) != value {
			return
		}
		_ = exec.Command("tmux", untagArgs(pane)...).Run()
	}
}
