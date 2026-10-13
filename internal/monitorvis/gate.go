package monitorvis

import (
	"strings"
	"syscall"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/upid"
)

// Gate is one monitor's side of the visibility rule: it tags the monitor's
// pane so RunWake can find it, answers the safety-net poll, and keeps the hooks
// installed. It satisfies liveview.Visibility.
type Gate struct {
	pane string
	bin  string
	// tag is the value written to TagOption, or "" when the pane could not be
	// tagged — no UPID on this platform, or tmux refused. An untagged monitor
	// still works; it just learns of a switch at its next poll instead of at
	// once.
	tag string
}

// NewGate returns the gate for the monitor in pane, or nil outside tmux, where
// a monitor is always visible and nothing gates it.
func NewGate(pane string) *Gate {
	if pane == "" {
		return nil
	}
	return &Gate{pane: pane, bin: hookBinary()}
}

// Start tags the pane, makes sure the hooks are installed, and reports whether
// the monitor is visible now. A missing hook here is the ordinary first start
// on a server, so it is installed without a fault.
//
// The caller must already be listening for SIGUSR1/SIGUSR2: once the tag is
// written, a window switch anywhere on the server signals this process.
func (g *Gate) Start() (visible bool) {
	if self, err := upid.Self(); err == nil {
		value := self.String()
		if _, err = runTmux(tagArgs(g.pane, value)...); err == nil {
			g.tag = value
		}
	}
	return g.poll(false)
}

// Check is the safety-net poll: one tmux invocation that answers visibility
// and shows the hooks, which it reinstalls — recording WARN-0034 — when
// something has removed them.
func (g *Gate) Check() (visible bool) {
	return g.poll(true)
}

// Missed logs that a poll disagreed with the state the last hook signal set.
// One missed hook corrects itself at that poll, so it is a log line and not a
// fault.
func (g *Gate) Missed(visible bool) {
	monitor.LogMonitorMissedHook(g.pane, visible)
}

// Close removes the tag — only while it still holds this process's UPID, so a
// replacement monitor that tagged the pane first keeps its tag.
func (g *Gate) Close() {
	if g.tag == "" {
		return
	}
	out, err := runTmux(readTagArgs(g.pane)...)
	if err != nil || strings.TrimSpace(out) != g.tag {
		return
	}
	_, _ = runTmux(untagArgs(g.pane)...)
}

// poll runs the one invocation and acts on it. When tmux cannot answer, the
// monitor counts as visible: drawing for nobody costs CPU, while hiding from
// somebody loses the view.
func (g *Gate) poll(missingIsFault bool) (visible bool) {
	out, err := runTmux(pollArgs(g.pane)...)
	if err != nil {
		record(faults.Fault{
			Code:        faults.ErrCodeMonitorVisibilityUnreadable,
			Source:      "monitorvis",
			Fingerprint: "monitor-visibility:" + serverKey(),
			Summary:     "a monitor could not ask tmux whether its window is visible",
			Detail:      err.Error(),
			Fields:      map[string]any{"pane": g.pane, "server": serverKey()},
		})
		return true
	}
	s := parsePoll(out)
	// Before the install: a stale entry is replaced, not kept beside the new.
	removeStaleHooks(s)
	if missing := s.missingHooks(); len(missing) > 0 {
		if missingIsFault {
			record(faults.Fault{
				Code:        faults.ErrCodeMonitorHooksMissing,
				Source:      "monitorvis",
				Fingerprint: "monitor-hooks-missing:" + serverKey(),
				Summary:     "monitor window-switch hooks were missing and were reinstalled: " + strings.Join(missing, ", "),
				Fields:      map[string]any{"pane": g.pane, "server": serverKey(), "missing": missing},
			})
		}
		_ = installMissing(g.bin, missing)
	}
	// Also every poll, not only after an install of this monitor's own: a
	// duplicate whose read-back failed is removed here.
	removeDuplicateHooks(s)
	if s.self == "" {
		return true
	}
	return s.visible[s.self]
}

// RunWake implements `endless-go monitor-wake`, the command the hooks run.
//
// It must be fast and must never fail loudly: tmux shows a run-shell command's
// failure to whoever is looking, and nobody asked for this one. So it always
// exits 0, and a tmux error is recorded (WARN-0033) instead of printed.
// Arguments are ignored.
func RunWake(args []string) {
	if serverKey() == "" {
		return
	}
	out, err := runTmux(wakeArgs()...)
	if err != nil {
		record(faults.Fault{
			Code:        faults.ErrCodeMonitorVisibilityUnreadable,
			Source:      "monitorvis",
			Fingerprint: "monitor-visibility:" + serverKey(),
			Summary:     "monitor-wake could not ask tmux which windows are visible",
			Detail:      err.Error(),
			Fields:      map[string]any{"server": serverKey()},
		})
		return
	}
	show, hide := WakeTargets(parseWake(out))
	signalAll(show, syscall.SIGUSR1)
	signalAll(hide, syscall.SIGUSR2)
}

// signalAll sends sig to each process that is still the one its tag names. A
// tag can outlive its monitor — a pane killed mid-exit — and its pid can by
// now belong to anything.
func signalAll(ids []upid.UPID, sig syscall.Signal) {
	for _, id := range ids {
		if !alive(id) {
			continue
		}
		_ = kill(id.PID, sig)
	}
}

// alive and kill are the process seams, so a test can see which pid would get
// which signal without signalling anything.
var (
	alive = upid.UPID.Alive
	kill  = syscall.Kill
)
