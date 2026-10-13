// Package monitorvis decides whether a live monitor can be seen, and wakes the
// monitors whose answer changed (E-2229).
//
// Every `session monitor` and `project monitor` pane used to recompute every
// two seconds whether or not anyone could see it. A person sees one window at a
// time, so nearly all of that work was wasted, and `task spawn` puts a monitor
// in every window it creates. ED-1606 settled the rule this package implements:
//
//   - A monitor is VISIBLE when its window is the active window of some client
//     attached to its tmux server. macOS app focus is irrelevant, and several
//     monitors may be visible at once. Outside tmux a monitor is always visible.
//   - The primary signal is tmux hooks. On every window switch, session switch,
//     attach and detach, tmux runs `endless-go monitor-wake` (RunWake), which
//     asks tmux once which windows are on screen and signals every tagged
//     monitor: SIGUSR1 to the visible, SIGUSR2 to the rest. A hidden monitor
//     therefore spends nothing of its own on a window switch.
//   - The safety net is each monitor asking the same question itself every
//     SafetyNet (Gate.Check), which also puts the hooks back if they are gone.
//
// Every tmux argv here is built by a pure function, in the style of
// internal/monitor/tmux_pane.go, and every tmux child goes through
// monitor.TmuxCommand so the ENDLESS_MONITOR_PROFILE diagnostic counts it.
package monitorvis

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/upid"
)

// TagOption is the PANE option a live monitor sets on its own pane while it
// runs. Its value is the monitor's UPID, so a wake never signals a process that
// merely inherited a dead monitor's pid.
//
// It is separate from sessionmonitorcmd.TagOption on purpose: that tag means
// "respawn me as a session monitor", which a project monitor must never be,
// while this one means only "signal me when my visibility changes", which both
// kinds want.
const TagOption = "@endless_monitor_upid"

// WakeVerb is the endless-go subcommand the hooks run. It is also the marker
// that recognizes our hook among a user's own: any hook command naming it is
// ours, whatever binary path it was installed with.
const WakeVerb = "monitor-wake"

// HookNames are the global tmux hooks that can change which windows are on
// screen.
var HookNames = []string{
	"session-window-changed",
	"client-session-changed",
	"client-attached",
	"client-detached",
}

// Line prefixes that tell apart the outputs of the commands chained into one
// tmux invocation.
const (
	selfPrefix   = "self "
	clientPrefix = "client "
	panePrefix   = "pane "
)

// tagArgs builds `tmux set-option -p -t <pane> @endless_monitor_upid <value>`.
func tagArgs(pane, value string) []string {
	return []string{"set-option", "-p", "-t", pane, TagOption, value}
}

// untagArgs builds `tmux set-option -p -u -t <pane> @endless_monitor_upid`.
func untagArgs(pane string) []string {
	return []string{"set-option", "-p", "-u", "-t", pane, TagOption}
}

// readTagArgs builds `tmux show-options -p -v -t <pane> @endless_monitor_upid`.
func readTagArgs(pane string) []string {
	return []string{"show-options", "-p", "-v", "-t", pane, TagOption}
}

// listClientsArgs builds `list-clients` printing each client's current window.
func listClientsArgs() []string {
	return []string{"list-clients", "-F", clientPrefix + "#{client_session} #{window_id}"}
}

// pollArgs builds the ONE tmux invocation a safety-net poll costs: the pane's
// own window (re-read every time, since a pane can be moved to another
// window), every client's current window, and the four global hooks.
func pollArgs(pane string) (args []string) {
	args = []string{"display-message", "-p", "-t", pane, selfPrefix + "#{window_id}", ";"}
	args = append(args, listClientsArgs()...)
	for _, hook := range HookNames {
		args = append(args, ";", "show-hooks", "-g", hook)
	}
	return args
}

// wakeArgs builds the one tmux invocation a wake costs: every pane's window and
// monitor tag, and every client's current window.
func wakeArgs() []string {
	return append([]string{"list-panes", "-a", "-F", panePrefix + "#{window_id} #{" + TagOption + "}", ";"},
		listClientsArgs()...)
}

// hookCommand is the command each hook runs. -b, because a hook that waited on
// it would hold up the window switch it is reporting.
func hookCommand(bin string) string {
	return fmt.Sprintf("run-shell -b '%s %s'", bin, WakeVerb)
}

// installHookArgs builds `tmux set-hook -ga <hook> <command>`. -a APPENDS to the
// hook's array, so a user's own hook on the same name is neither replaced by
// ours nor able to replace ours.
func installHookArgs(hook, bin string) []string {
	return []string{"set-hook", "-ga", hook, hookCommand(bin)}
}

// snapshot is one parsed poll.
type snapshot struct {
	self    string          // the monitor pane's window id
	visible map[string]bool // window ids some client is showing
	// entries maps each hook name to its array entries running WakeVerb, as
	// show-hooks names them (`session-window-changed[2]`), in index order.
	entries map[string][]string
	// stale lists entries of ours whose binary no longer exists — installed
	// by an endless-go since moved or deleted, such as a removed worktree's
	// bin/. They are not counted in entries: a hook that can only fail is a
	// hook that is missing.
	stale []string
}

// parsePoll reads the output of pollArgs. show-hooks prints one line per entry
// of a hook's array, `<name>[<n>] <command>`, and just the bare name when the
// hook has none.
func parsePoll(out string) (s snapshot) {
	s.visible = map[string]bool{}
	s.entries = map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, selfPrefix):
			s.self = strings.TrimSpace(strings.TrimPrefix(line, selfPrefix))
		case strings.HasPrefix(line, clientPrefix):
			addClientWindow(s.visible, line)
		default:
			entry, command, ok := strings.Cut(line, " ")
			if !ok || !isWakeHook(command) {
				continue
			}
			if !hookBinaryExists(command) {
				s.stale = append(s.stale, entry)
				continue
			}
			name, _, _ := strings.Cut(entry, "[")
			s.entries[name] = append(s.entries[name], entry)
		}
	}
	return s
}

// addClientWindow records the window of one `client <session> <window>` line.
func addClientWindow(visible map[string]bool, line string) {
	fields := strings.Fields(strings.TrimPrefix(line, clientPrefix))
	if len(fields) == 0 {
		return
	}
	visible[fields[len(fields)-1]] = true
}

// isWakeHook reports whether a hook command is ours. The verb rather than the
// whole command line, because the binary path differs per machine and install;
// as a whole word, so a user's own `monitor-wakeful` is not taken for it.
func isWakeHook(command string) bool {
	return wakeHookPattern.MatchString(command)
}

// hookBinaryExists reports whether the binary a wake hook runs is still there:
// the word before WakeVerb, unquoted. Only an absolute path can be checked; a
// bare name is resolved by tmux's PATH and is taken as present.
func hookBinaryExists(command string) bool {
	loc := wakeHookPattern.FindStringIndex(command)
	if loc == nil {
		return true
	}
	fields := strings.Fields(command[:loc[0]])
	if len(fields) == 0 {
		return true
	}
	bin := strings.TrimLeft(fields[len(fields)-1], `"'`)
	if !filepath.IsAbs(bin) {
		return true
	}
	_, err := os.Stat(bin)
	return err == nil
}

// wakeHookPattern matches WakeVerb as an argument: after whitespace, and
// before whitespace, a closing quote or the end.
var wakeHookPattern = regexp.MustCompile(`\s` + regexp.QuoteMeta(WakeVerb) + `(["'\s]|$)`)

// missingHooks returns the HookNames a snapshot found without our command, in
// HookNames order.
func (s snapshot) missingHooks() (missing []string) {
	for _, hook := range HookNames {
		if len(s.entries[hook]) == 0 {
			missing = append(missing, hook)
		}
	}
	return missing
}

// monitorPane is one tagged pane found by a wake.
type monitorPane struct {
	window string
	id     upid.UPID
}

// parseWake reads the output of wakeArgs. A pane whose tag is empty or does not
// parse is not a monitor.
func parseWake(out string) (panes []monitorPane, visible map[string]bool) {
	visible = map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, clientPrefix):
			addClientWindow(visible, line)
		case strings.HasPrefix(line, panePrefix):
			window, tag, _ := strings.Cut(strings.TrimPrefix(line, panePrefix), " ")
			id, err := upid.Parse(tag)
			if err != nil {
				continue
			}
			panes = append(panes, monitorPane{window: window, id: id})
		}
	}
	return panes, visible
}

// WakeTargets splits the tagged monitors into those to show and those to hide.
// Pure, so which pid gets which signal is testable without tmux.
func WakeTargets(panes []monitorPane, visible map[string]bool) (show, hide []upid.UPID) {
	for _, p := range panes {
		if visible[p.window] {
			show = append(show, p.id)
			continue
		}
		hide = append(hide, p.id)
	}
	return show, hide
}

// runTmux runs one tmux invocation and returns its stdout, folding tmux's
// stderr into the error. A var so tests can answer for tmux.
var runTmux = func(args ...string) (out string, err error) {
	var stdout, stderr bytes.Buffer
	cmd := monitor.TmuxCommand(args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), err
}

// record is faults.Record, held as a var so tests can capture what is raised.
var record = faults.Record

// serverKey names the tmux server this process talks to, for fingerprints: one
// broken server is one incident however many monitors trip over it.
func serverKey() string {
	socket, _, _ := strings.Cut(os.Getenv("TMUX"), ",")
	return socket
}

// InstallHooks appends our command to each named hook. It is the stopgap
// install (E-2229): a monitor installs into the server it runs on at startup
// and at every safety-net poll, and `endless tmux apply` does the same.
//
// STOPGAP. ED-1607 still governs: the intended home for these hooks is the
// Endless-owned tmux config (E-1850, E-2086, E-1809). When that lands, the
// config declares them and this runtime install goes away.
//
// A failure is recorded (WARN-0035) and returned.
func InstallHooks(bin string, hooks []string) (err error) {
	for _, hook := range hooks {
		args := installHookArgs(hook, bin)
		_, err = runTmux(args...)
		if err != nil {
			record(faults.Fault{
				Code:        faults.ErrCodeMonitorHooksInstallFailed,
				Source:      "monitorvis",
				Fingerprint: "monitor-hooks-install:" + serverKey(),
				Summary:     "tmux refused to install the monitor window-switch hook " + hook,
				Detail:      err.Error(),
				Fields:      map[string]any{"args": args, "server": serverKey()},
			})
			return err
		}
	}
	return nil
}

// showHooksArgs builds one invocation listing every one of HookNames.
func showHooksArgs() (args []string) {
	for _, hook := range HookNames {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(args, "show-hooks", "-g", hook)
	}
	return args
}

// EnsureHooks installs whichever of HookNames lack our command, reading the
// server's hooks first so a second call adds nothing. It is what `endless tmux
// apply` calls.
func EnsureHooks(bin string) (err error) {
	var out string
	var snap snapshot

	out, err = runTmux(showHooksArgs()...)
	if err != nil {
		record(faults.Fault{
			Code:        faults.ErrCodeMonitorHooksInstallFailed,
			Source:      "monitorvis",
			Fingerprint: "monitor-hooks-install:" + serverKey(),
			Summary:     "tmux refused to list hooks before installing the monitor window-switch hooks",
			Detail:      err.Error(),
			Fields:      map[string]any{"server": serverKey()},
		})
		goto end
	}
	snap = parsePoll(out)
	removeStaleHooks(snap)
	err = installMissing(bin, snap.missingHooks())

end:
	return err
}

// installMissing installs the missing hooks, then reads them back and removes
// any duplicate of ours.
//
// The read-back is what makes concurrent installs converge. Monitors started
// together — a server restored with many windows at once — each find the hooks
// missing and each append them. Whichever of them installs LAST reads back
// after every append, so it sees all the copies and removes the extras; left
// alone, every window switch would run the wake once per copy.
func installMissing(bin string, missing []string) (err error) {
	var out string

	if len(missing) == 0 {
		goto end
	}
	err = InstallHooks(bin, missing)
	if err != nil {
		goto end
	}
	out, err = runTmux(showHooksArgs()...)
	if err != nil {
		// The install itself succeeded; a duplicate left behind is removed by
		// the next poll.
		err = nil
		goto end
	}
	removeDuplicateHooks(parsePoll(out))

end:
	return err
}

// removeStaleHooks unsets the entries of ours whose binary is gone.
func removeStaleHooks(s snapshot) {
	for _, entry := range s.stale {
		_, _ = runTmux("set-hook", "-gu", entry)
	}
}

// removeDuplicateHooks unsets every entry of ours after the first on each hook.
// Every process keeps the LOWEST index, so two removing at once agree on which
// survives, and an index another has already unset is a harmless refusal.
func removeDuplicateHooks(s snapshot) {
	for _, entries := range s.entries {
		for _, entry := range entries[min(1, len(entries)):] {
			_, _ = runTmux("set-hook", "-gu", entry)
		}
	}
}

// hookBinary is the path the hooks run: this process's own executable, which
// is the endless-go a monitor or `tmux apply` was started from.
func hookBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return "endless-go"
	}
	return exe
}
