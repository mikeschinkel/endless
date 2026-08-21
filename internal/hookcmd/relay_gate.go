package hookcmd

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// relay_gate.go holds the Stop gate that makes `endless task report` an
// enforcer instead of a suggestion.
//
// OWNED BY E-1953. The file was built by E-1901, parked by E-1911, and left
// orphaned when E-1911 landed — no task owned it and nothing fired it. E-1953
// adopts it explicitly: the machinery E-1901 built is exactly what the minimizer
// needs, because both enforce the same shape of claim (the agent owes the user
// one specific string as its final message) and differ only in where that string
// comes from. E-1901 rendered it from fields the agent volunteered; E-1953 gets
// it by handing the agent's whole draft to an adversarial minimizer.
//
// That difference is why the gate can be live now and could not be then. Under
// E-1911's append model an equality check would have bounced every correct
// reply, since the agent's own answer was deliberately unconstrained. Under the
// minimizer the output IS the reply, so equality is once again the right test.
//
// The gate catches two distinct violations, and the second is the one that
// matters:
//
//   - The agent reported, then embellished. Caught by comparing the final
//     message against the checkpoint.
//   - The agent never reported at all. This is the trivial bypass — an
//     enforcement that only fires once you opt in enforces nothing — and it is
//     new here, because under E-1901 "no checkpoint" was the fail-open case.
//
// The `relay` gate KIND keeps its slug and its columns. The name still describes
// the job accurately (the agent relays the minimizer's output verbatim) and
// renaming it would churn the schema, the enum, and the integrity check for no
// behavior change.
//
// Everything except enforceReportGate is pure: no DB, no I/O, no process state.
// The comparison is the part that must be exactly right, so it is testable
// without infrastructure.

// relayFenceRe matches a line that is nothing but a markdown code fence, with an
// optional language tag. Such lines are dropped from BOTH sides before
// comparison: wrapping the verify command in ``` is formatting, not prose, and
// bouncing it would be a false positive. False positives are the one thing this
// gate cannot afford — an agent that gets bounced for correct behavior learns
// the gate is noise, and the deterrent dies with the trust.
var relayFenceRe = regexp.MustCompile("^`{3,}[a-zA-Z0-9_+-]*$")

// normalizeRelayText reduces a message to the form the equality check compares:
// the sequence of its non-empty lines, each stripped of surrounding whitespace,
// with code fences removed.
//
// Blank lines and indentation are discarded outright rather than preserved,
// because neither carries meaning the gate should enforce while both vary freely
// in how an agent reproduces a block. Any difference the gate keys on must be a
// difference that MATTERS — spacing does not, and treating it as a violation
// produces bounces the agent cannot learn from.
//
// What survives is every line containing words, on both sides. That is the one
// property the gate rests on: prose cannot be normalized away.
func normalizeRelayText(s string) string {
	raw := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		bare := strings.TrimSpace(line)
		if bare == "" || relayFenceRe.MatchString(bare) {
			continue
		}
		out = append(out, bare)
	}
	return strings.Join(out, "\n")
}

// relayVerdict compares an agent's actual final message against the minimized
// text and reports whether the turn may end.
//
// Compliant is deliberately two cases, not one:
//
//   - normalized-equal — the agent relayed the minimized output and nothing else.
//   - normalized-empty — the agent said nothing at all. Silence is not what the
//     minimizer asked for, but it is not the violation this gate exists to catch:
//     the offense is APPENDING, and a gate that bounced silence would punish an
//     agent for under-speaking while trying to obey.
//
// extra counts the lines present in the actual message that the minimized text
// does not contain. It is what makes the bounce reason specific ("appended 4
// lines beyond the report") rather than a generic mismatch complaint — the agent
// can see exactly how much it added, and so can the user.
func relayVerdict(sanctioned, actual string) (extra int, ok bool) {
	return relayVerdictAnyOf([]string{sanctioned}, actual)
}

// relayVerdictAnyOf is relayVerdict over a SET of acceptable texts: the turn may
// end if the final message matches any one of them.
//
// A/B is why. On a paired turn the command emits both variants as one block, and
// that block is what the agent owes — but the two later shapes this design
// leaves room for (an AskUserQuestion preview, a left/right TUI) both have the
// agent send the WINNING variant instead. Accepting any of N is the whole
// difference between those shapes being a schema change and being a no-op, so it
// is bought here while it costs one loop.
//
// When nothing matches, the reported `extra` is the SMALLEST divergence across
// the set. Reporting the largest, or the first, would tell an agent that added
// one line to variant B that it added forty — a bounce reason that misdescribes
// the violation teaches the wrong correction.
func relayVerdictAnyOf(accepted []string, actual string) (extra int, ok bool) {
	if len(accepted) == 0 {
		return 0, true
	}
	best := -1
	for _, want := range accepted {
		n, matched := relayVerdictOne(want, actual)
		if matched {
			return 0, true
		}
		if best < 0 || n < best {
			best = n
		}
	}
	return best, false
}

func relayVerdictOne(sanctioned, actual string) (extra int, ok bool) {
	wantText := normalizeRelayText(sanctioned)
	gotText := normalizeRelayText(actual)
	if gotText == "" || gotText == wantText {
		return 0, true
	}
	want := make(map[string]int)
	for _, line := range strings.Split(wantText, "\n") {
		want[line]++
	}
	for _, line := range strings.Split(gotText, "\n") {
		if want[line] > 0 {
			want[line]--
			continue
		}
		extra++
	}
	// The message differs but adds no unaccounted line — it dropped or reordered
	// sanctioned content instead. Still a violation (the output was not relayed
	// verbatim), so report at least one line's worth of divergence rather than
	// returning a contradictory "0 extra lines, blocked".
	if extra == 0 {
		extra = 1
	}
	return extra, false
}

// relayBlockReason is the text Claude reads when it embellished a report. It
// names the act, quantifies it, and gives the two ways out — resend clean, or
// put the extra content through the minimizer, which is the channel that exists
// precisely so an agent with something more to say is not left choosing between
// silence and defiance.
func relayBlockReason(extra int, sanctioned string) string {
	return fmt.Sprintf(
		"BLOCKED: you added %s beyond the output of `endless task report`. That "+
			"output IS your message — sending it verbatim is the whole job, and "+
			"adding to it is the exact habit this gate exists to catch.\n\n"+
			"Resend your final message as EXACTLY this and nothing else:\n\n"+
			"%s\n\n"+
			"If what you added genuinely belongs in the reply, do not paste it "+
			"alongside — write your full draft (including that content) to a file "+
			"and re-run `endless task report --draft-file <path>`, then send the "+
			"new output verbatim. You get one such appeal per turn.",
		pluralLines(extra), sanctioned,
	)
}

// reportMissingReason is the text Claude reads when it never ran the command at
// all. It is the bypass case, so it does not argue — it states the contract and
// hands over the exact invocation.
//
// It asks for the draft the agent ALREADY wrote rather than for a summary. Not
// because a shorter draft would be worse — E-2030 settled that an agent which
// cuts its own output has achieved the objective — but because composing a
// summary to satisfy the block is a second artifact, and the restatement tax of
// producing one is the very failure that made the old field-based command leak
// content into prose.
func reportMissingReason() string {
	return "BLOCKED: this turn has a reply but never ran `endless task report`.\n\n" +
		"Every turn that speaks to the user goes through the minimizer. Do this:\n\n" +
		"  1. Write the reply you were about to send, exactly as you drafted " +
		"it, to a file.\n" +
		"  2. Run `endless task report [<task-id>] --draft-file <path>`. The task " +
		"id is optional; omit it when nothing is claimed.\n" +
		"  3. Send that command's output as your entire final message, verbatim.\n\n" +
		"Send the draft you already have rather than composing a new one for " +
		"this block — a reply written to satisfy the gate is a different reply " +
		"than the one the user was owed."
}

// relaySystemMessage is the user-visible half of the embellishment bounce. The
// block reason goes to Claude; this goes to the user, so the violation is named
// where BOTH can see it. That is the point of the design: a catch the agent
// could absorb privately is just another instruction to reinterpret, while one
// the user watches is a cost.
func relaySystemMessage(extra int) string {
	return fmt.Sprintf(
		"Endless: blocked an embellished reply — the session added %s beyond its "+
			"`task report` output and was told to resend it verbatim.",
		pluralLines(extra),
	)
}

// reportMissingSystemMessage is the user-visible half of the never-called
// bounce.
func reportMissingSystemMessage() string {
	return "Endless: blocked a reply that skipped `task report` — the session was " +
		"told to put its draft through the minimizer."
}

// relayExhaustedMessage is shown to the user when the embellishment budget runs
// out and the gate stops holding the turn.
func relayExhaustedMessage(bounces int) string {
	return fmt.Sprintf(
		"Endless: the session added to its `task report` output %s and did not "+
			"resend it verbatim; letting the turn end. The final message below is "+
			"NOT the minimized report.",
		pluralTimes(bounces),
	)
}

// reportExhaustedMessage is shown to the user when the never-called budget runs
// out.
//
// Surrender is announced, never silent — the authoring session was explicit that
// a silent livelock and a silent surrender look identical from outside, and the
// second is how a gate rots unnoticed. Whoever reads this knows the reply below
// it went un-minimized, which is the difference between a known limitation and a
// gate everyone believes is working.
func reportExhaustedMessage(bounces int) string {
	return fmt.Sprintf(
		"Endless: the session did not run `task report` after %s; letting the turn "+
			"end. The final message below did NOT go through the minimizer.",
		pluralTimes(bounces),
	)
}

func pluralTimes(n int) string {
	if n == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", n)
}

func pluralLines(n int) string {
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}

// stopBlock is the Stop hook's block response. `decision`/`reason` hold the
// turn open and go to Claude; `systemMessage` is the user-visible channel. Both
// are populated on a bounce by design — a violation only the agent sees is one
// it can privately reinterpret, which is precisely the failure mode that made
// E-1803's nudge insufficient.
type stopBlock struct {
	Decision      string `json:"decision,omitempty"`
	Reason        string `json:"reason,omitempty"`
	SystemMessage string `json:"systemMessage,omitempty"`
}

// enforceReportGate is the impure half: it decides whether the turn ending may
// end, and either clears the checkpoint or emits a block response.
//
// Returns handled=true only when it has written a response to stdout, so the
// caller knows to stop — the hook's stdout is a single JSON document and a
// second write would corrupt it.
//
// Fails OPEN on every case where it cannot prove a violation: an unregistered
// project, a project that switched the gate off, a subagent, no session row, a
// `$FULL` license, an empty last_assistant_message, or a DB error. Blocking on
// any of those would hold a turn hostage over the gate's own ignorance, and an
// enforcement mechanism that strands sessions gets switched off — which protects
// nothing.
func enforceReportGate(projectID int64, isRegistered bool, payload claudePayload) (handled bool, err error) {
	// Unregistered projects are outside Endless entirely. Gating them would mean
	// a tool the user never opted into holding turns in a directory it does not
	// track.
	if !isRegistered {
		return false, nil
	}

	// Never gate an Agent-tool subagent. Its final message is a return value to
	// the parent agent, not a handoff to a user — nothing may sit between an
	// agent and its subagent, and the parent's own reply (the thing the user
	// actually reads) is already gated.
	if payload.AgentID != "" {
		return false, nil
	}

	session, err := monitor.GetActiveSession(payload.SessionID)
	if err != nil || session == nil {
		return false, err
	}

	// Burn the `$FULL` license FIRST, before any other early return can skip it.
	// A license that survived a turn it did not cover would be indistinguishable
	// from the gate being off, and the user has no way to tell which they are in.
	exempt, err := monitor.ConsumeReportExemption(session.ID)
	if err != nil {
		log.Printf("consuming report exemption: %v", err)
	}
	if exempt {
		// The licensed reply does not go through the minimizer — routing it
		// through would contradict the license. Any checkpoint from earlier in
		// the turn is retired rather than enforced.
		if _, cerr := monitor.ClearRelayCheckpoint(session.ID, "relay_exempt"); cerr != nil {
			log.Printf("clearing checkpoint under $FULL: %v", cerr)
		}
		return false, nil
	}

	// Same resolution the SessionStart rule uses, so a project is never told to
	// use a channel that will not gate it, nor gated without being told.
	if !reportChannelOn(projectID, isRegistered, payload.CWD) {
		return false, nil
	}

	// An empty final message is ambiguous in a way nothing here can resolve: it
	// is either a tool-only turn with nothing to minimize, an agent that
	// genuinely said nothing, or a Claude Code build that does not populate the
	// field. None of those is a violation this gate can prove, so all three pass.
	//
	// For the embellishment case the checkpoint stays OPEN rather than being
	// cleared: clearing on empty would mean that on a build which never populates
	// the field, every checkpoint closes as "complied" and the gate silently
	// reports success while enforcing nothing — the one failure mode worse than
	// not shipping the gate. The next user prompt retires it harmlessly.
	if strings.TrimSpace(payload.LastAssistantMessage) == "" {
		return false, nil
	}

	cp, bounces, found, err := monitor.PendingReportCheckpoint(session.ID)
	if err != nil {
		return false, err
	}
	if !found {
		return enforceReportCalled(session.ID)
	}
	sanctioned := cp.Owed

	extra, ok := relayVerdictAnyOf(cp.Accepted, payload.LastAssistantMessage)
	if ok {
		_, cerr := monitor.ClearRelayCheckpoint(session.ID, "relay_complied")
		return false, cerr
	}

	// Budget exhausted: clear and let the turn end, but say so out loud.
	if bounces >= monitor.RelayBounceLimit {
		if _, cerr := monitor.ClearRelayCheckpoint(session.ID, "relay_exhausted"); cerr != nil {
			return false, cerr
		}
		return true, json.NewEncoder(os.Stdout).Encode(stopBlock{
			SystemMessage: relayExhaustedMessage(bounces),
		})
	}

	if _, berr := monitor.BumpRelayBounce(session.ID); berr != nil {
		return false, berr
	}
	return true, json.NewEncoder(os.Stdout).Encode(stopBlock{
		Decision:      "block",
		Reason:        relayBlockReason(extra, sanctioned),
		SystemMessage: relaySystemMessage(extra),
	})
}

// enforceReportCalled handles the bypass: a turn that produced a reply without
// ever running `task report`.
//
// Its loop guard is a session counter rather than session_gates.bounces, because
// the defining feature of this case is that no gate row exists to count on. The
// counter resets when the user next speaks (StageUserPrompt), so the budget is
// per-turn rather than per-session.
func enforceReportCalled(sessionID int64) (handled bool, err error) {
	bounces, err := monitor.BumpReportBounce(sessionID)
	if err != nil {
		return false, err
	}
	if bounces > monitor.ReportBounceLimit {
		return true, json.NewEncoder(os.Stdout).Encode(stopBlock{
			SystemMessage: reportExhaustedMessage(bounces - 1),
		})
	}
	return true, json.NewEncoder(os.Stdout).Encode(stopBlock{
		Decision:      "block",
		Reason:        reportMissingReason(),
		SystemMessage: reportMissingSystemMessage(),
	})
}

// clearRelayCheckpointForNewTurn drops any unconsumed checkpoint when the user
// submits a new prompt. A checkpoint is a debt owed within ONE turn; once the
// user has spoken again, the moment it was recorded for has passed and holding
// it would bounce a later, unrelated reply.
func clearRelayCheckpointForNewTurn(payload claudePayload) {
	session, err := monitor.GetActiveSession(payload.SessionID)
	if err != nil || session == nil {
		return
	}
	if _, err := monitor.ClearRelayCheckpoint(session.ID, "relay_superseded"); err != nil {
		log.Printf("clearing relay checkpoint for new turn: %v", err)
	}
}
