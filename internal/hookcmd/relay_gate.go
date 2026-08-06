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

// relay_gate.go holds the verbatim-report-relay Stop gate (E-1901) — the
// enforcement layer under E-1803's compose-time nudge.
//
// E-1803 injects "relay the report verbatim, add nothing" at PostToolUse and is
// explicit that it is a nudge, not a gate: the harness always lets the model
// author its final message. Agents override it anyway, because appending does
// not feel like defiance — it feels like thoroughness. An instruction cannot fix
// a disguise; only a detector that catches the act and NAMES it can, which is
// what this file is.
//
// Everything here is pure: no DB, no I/O, no process state. The comparison is
// the part that must be exactly right, so it is testable without infrastructure.
// The impure half (reading the checkpoint, emitting the block) lives in
// claude.go's Stop branch.

// relayFenceRe matches a line that is nothing but a markdown code fence, with an
// optional language tag. Such lines are dropped from BOTH sides before
// comparison: wrapping the verify command in ``` is formatting, not prose, and
// bouncing it would be a false positive. False positives are the one thing this
// gate cannot afford — an agent that gets bounced for correct behavior learns
// the gate is noise, and the deterrent dies with the trust.
var relayFenceRe = regexp.MustCompile("^`{3,}[a-zA-Z0-9_+-]*$")

// relayMarkerRe matches the BEGIN/END REPORT delimiter lines the report command
// prints around the sanctioned block. They mark the block's extent for the
// agent; they are not part of the message. Dropped from both sides so an agent
// that copies the block *with* its markers is compliant rather than bounced.
var relayMarkerRe = regexp.MustCompile(`^-{3,}\s*(BEGIN|END) REPORT\s*-{3,}$`)

// normalizeRelayText reduces a message to the form the equality check compares:
// the sequence of its non-empty lines, each stripped of surrounding whitespace,
// with code fences and block markers removed.
//
// Blank lines and indentation are discarded outright rather than preserved,
// because neither carries meaning in a sanctioned block (the renderer joins
// single newlines) while both vary freely in how an agent formats a relay. Any
// difference the gate keys on must be a difference that MATTERS — spacing does
// not, and treating it as a violation produces bounces the agent cannot learn
// from.
//
// What survives is every line containing words, on both sides. That is the one
// property the gate rests on: prose cannot be normalized away.
func normalizeRelayText(s string) string {
	raw := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		bare := strings.TrimSpace(line)
		if bare == "" || relayFenceRe.MatchString(bare) || relayMarkerRe.MatchString(bare) {
			continue
		}
		out = append(out, bare)
	}
	return strings.Join(out, "\n")
}

// relayVerdict compares an agent's actual final message against the sanctioned
// report text and reports whether the turn may end.
//
// Compliant is deliberately two cases, not one:
//
//   - normalized-equal — the agent relayed the report and nothing else.
//   - normalized-empty — the agent said nothing at all. Silence is not what the
//     report asked for, but it is not the violation this gate exists to catch:
//     the offense is APPENDING, and a gate that bounced silence would punish an
//     agent for under-speaking while trying to obey.
//
// extra counts the lines present in the actual message that the sanctioned text
// does not contain. It is what makes the bounce reason specific ("appended 4
// lines beyond the report") rather than a generic mismatch complaint — the agent
// can see exactly how much it added, and so can the user.
func relayVerdict(sanctioned, actual string) (extra int, ok bool) {
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
	// sanctioned content instead. Still a violation (the report was not relayed
	// verbatim), so report at least one line's worth of divergence rather than
	// returning a contradictory "0 extra lines, blocked".
	if extra == 0 {
		extra = 1
	}
	return extra, false
}

// relayBlockReason is the text Claude reads when the gate fires. It names the
// act, quantifies it, and gives the two ways out — resend clean, or route the
// extra content through the report where it belongs. The second matters: an
// agent bounced with no legitimate channel for what it wanted to say will
// rationalize appending it again.
func relayBlockReason(extra int, sanctioned string) string {
	return fmt.Sprintf(
		"BLOCKED: you appended %s beyond the sanctioned `endless task report` "+
			"output. The report IS the message — relaying it verbatim is the whole "+
			"job, and adding to it is the exact habit this gate exists to catch.\n\n"+
			"Resend your final message as EXACTLY this and nothing else:\n\n"+
			"%s\n\n"+
			"If what you added was a genuine open decision, a non-computable fact, "+
			"or the verify command, it does not belong in prose alongside the "+
			"report — re-run `endless task report <id> --json` with a "+
			"notes/questions/verify entry so it lands INSIDE the block, then relay "+
			"the new block.",
		pluralLines(extra), sanctioned,
	)
}

// relaySystemMessage is the user-visible half of the bounce. The block reason
// goes to Claude; this goes to the user, so the violation is named where BOTH
// can see it. That is the point of the design: a catch the agent could absorb
// privately is just another instruction to reinterpret, while one the user
// watches is a cost.
func relaySystemMessage(extra int) string {
	return fmt.Sprintf(
		"Endless: blocked an appended handoff — the session added %s beyond its "+
			"`task report` output and was told to resend the report verbatim.",
		pluralLines(extra),
	)
}

// relayExhaustedMessage is shown to the user when the bounce budget runs out and
// the gate stops holding the turn. It is deliberately not silent: the honest
// ceiling here is that a re-prompted model can keep drifting, and a gate that
// gave up quietly would misreport that ceiling as compliance.
func relayExhaustedMessage(bounces int) string {
	return fmt.Sprintf(
		"Endless: the session appended to its `task report` output %s and did not "+
			"resend it verbatim; letting the turn end. The final message below is "+
			"NOT the sanctioned report.",
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

// enforceRelayGate is the impure half: it reads the session's pending relay
// checkpoint, compares the turn's final message against it, and either clears
// the checkpoint (compliant) or emits the block response (violation).
//
// Returns handled=true only when it has written a response to stdout, so the
// caller knows to stop — the hook's stdout is a single JSON document and a
// second write would corrupt it.
//
// Fails OPEN throughout. Every early return here is a case where the gate cannot
// prove a violation: no session row, no pending checkpoint, an empty
// last_assistant_message (a tool-only turn, or a Claude Code build that does not
// send the field). Blocking on any of those would hold a turn hostage over the
// gate's own ignorance, and an enforcement mechanism that strands sessions gets
// switched off — which protects nothing.
func enforceRelayGate(payload claudePayload) (handled bool, err error) {
	// Never gate an Agent-tool subagent. Its final message is a return value to
	// the parent agent, not a handoff to the user — the report contract does not
	// apply, and blocking it would strand the parent waiting on a subagent that
	// is being told to relay a user-facing report nobody will read.
	if payload.AgentID != "" {
		return false, nil
	}

	session, err := monitor.GetActiveSession(payload.SessionID)
	if err != nil || session == nil {
		return false, err
	}
	sanctioned, bounces, found, err := monitor.PendingRelayCheckpoint(session.ID)
	if err != nil || !found {
		return false, err
	}

	// An empty final message is handled HERE as well as inside relayVerdict, and
	// the difference is the side effect, not the verdict — neither blocks.
	//
	// The string is ambiguous in a way nothing here can resolve: it is either an
	// agent that genuinely said nothing, or a Claude Code build that does not
	// populate last_assistant_message. Clearing on it would mean that on such a
	// build every checkpoint closes as "complied" and the gate silently reports
	// success while enforcing nothing — the one failure mode worse than not
	// shipping the gate. So the checkpoint stays OPEN; the debt is still owed,
	// and the next user prompt retires it harmlessly either way.
	if strings.TrimSpace(payload.LastAssistantMessage) == "" {
		return false, nil
	}

	extra, ok := relayVerdict(sanctioned, payload.LastAssistantMessage)
	if ok {
		_, cerr := monitor.ClearRelayCheckpoint(session.ID, "relay_complied")
		return false, cerr
	}

	// Budget exhausted: clear and let the turn end, but say so out loud. The
	// analysis is explicit that this gate cannot guarantee compliance, and a
	// silent surrender would misreport a violation as a clean handoff.
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

// clearRelayCheckpointForNewTurn drops any unconsumed checkpoint when the user
// submits a new prompt. A checkpoint is a debt owed within ONE turn; once the
// user has spoken again, the moment it was recorded for has passed and holding
// it would bounce a later, unrelated reply. This is also what keeps the
// `FULL STATUS` escape hatch working — that keyword arrives as a user prompt,
// which clears the gate before the licensed response is composed.
func clearRelayCheckpointForNewTurn(payload claudePayload) {
	session, err := monitor.GetActiveSession(payload.SessionID)
	if err != nil || session == nil {
		return
	}
	if _, err := monitor.ClearRelayCheckpoint(session.ID, "relay_superseded"); err != nil {
		log.Printf("clearing relay checkpoint for new turn: %v", err)
	}
}
