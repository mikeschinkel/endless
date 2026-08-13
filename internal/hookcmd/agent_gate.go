package hookcmd

import "github.com/mikeschinkel/endless/internal/agentenv"

// supportedAgent reports whether the harness running this session is one Endless
// supports (E-1962). Today that is Claude Code in a terminal, and nothing else.
//
// Detection lives in internal/agentenv, not here, because "which harness is
// this" is not a hook concern — E-1505 needs the same answer to stamp a platform
// on the session row, and `endless guide` needs it to tell an unsupported
// harness to ignore Endless entirely.
//
// The hooks are, for now, the ONLY thing gated on it. That is a deliberate
// scope: a hook fires inside the session and speaks to the agent, so it is where
// an unsupported harness gets told to obey a contract that will not be enforced
// for it. Commands the user runs by hand are not gated — an unsupported harness
// running `endless task add` is a person using a tool, not a session being
// mis-instructed.
func supportedAgent() bool {
	return agentenv.Supported()
}
