package hookcmd

import (
	"log"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// The `Notification` hook (E-2091).
//
// Endless hooked six Claude Code events and none of them reported that the
// harness was asking the user for permission, so a session sitting on a prompt
// read `working` — indistinguishable from one doing work. `Notification` is the
// event that reports it, and this file is the whole of the producer side.
//
// Not in setup.py's SYNC_EVENTS: the handler records a state and gates nothing,
// so it has no reason to block the harness while it runs.
//
// Why `Notification` rather than `PermissionRequest`, which is now a first-class
// event carrying tool_name and tool_use_id: PermissionRequest fires whenever a
// permission DECISION is needed, which an allowlist or another hook can resolve
// without the user ever seeing anything. `Notification` with
// notification_type=permission_prompt fires when the user is ACTUALLY prompted,
// which is the fact this records. `agent_needs_input` was checked too and is the
// wrong signal for a different reason — it is scoped to agent-team teammates
// about to go idle, not to a main session.

// The two `notification_type` values Endless models. The rest of the vocabulary
// is deliberately unnamed here — nothing is written for it, so there is nothing
// to name.
const (
	notifyPermissionPrompt = "permission_prompt"
	notifyIdlePrompt       = "idle_prompt"
)

// handleNotification records what a notification says about the session, and
// records nothing for a notification it does not model.
//
// That last clause is the rule, not a gap. `notification_type` carries at least
// a dozen documented values — auth, elicitation, quota, agent-team — and a
// notification Endless has no state for must not move the session: an
// unrecognised value is also the shape a harness change arrives in, and the
// safe response to one is to leave the row alone rather than guess.
//
// monitor.ParseTranscript is deliberately NOT called here. A notification is not
// a turn boundary and carries no new messages.
func handleNotification(payload claudePayload) error {
	switch payload.NotificationType {
	case notifyPermissionPrompt:
		// The user is being asked to approve a tool call. The session is blocked
		// mid-turn on a person.
		if err := monitor.PromptSession(payload.SessionID); err != nil {
			return err
		}
	case notifyIdlePrompt:
		// The input prompt sat untouched for 60 seconds, which is what `idle`
		// already says. Idempotent on a session that is already idle, so there
		// is nothing to guard against a repeat.
		//
		// runClaude's per-event WakeSession has already run by the time we get
		// here and may have lifted an idle session to `working`; this write
		// lands after it, so the row ends idle either way. That ordering is
		// worth knowing rather than worth changing: the wake belongs at the one
		// point every turn reaches, and carving an exception into it for one
		// event is how that helper would start carrying special cases.
		if err := monitor.IdleSession(payload.SessionID); err != nil {
			return err
		}
	}
	return nil
}

// clearPromptState returns a prompt-blocked session to `working` on the two
// events that mean the user answered: PostToolUse (the tool the prompt was
// about completed, so they approved) and UserPromptSubmit (they typed instead).
//
// Non-fatal, and that is a decision rather than laziness. `prompted` is in
// sessionstate.MayWrite, so a clear that fails costs a stale glyph on the
// attention board until the next Stop writes `idle` — cosmetic and
// self-correcting. Failing the hook over it would take the session down to fix
// a display.
func clearPromptState(payload claudePayload) {
	if err := monitor.ResumeFromPrompt(payload.SessionID); err != nil {
		log.Printf("clearing prompt state for session %s: %v", payload.SessionID, err)
	}
}
