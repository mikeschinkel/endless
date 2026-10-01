package refusal

import (
	"os"

	"github.com/mikeschinkel/endless/internal/agentenv"
)

// AudienceVar is the environment contract between the two halves of Endless
// (E-2159). The Python CLI decides the audience once, in the root group, from
// the harness environment plus `--agent`, `--agent-view` and `--format agent`;
// it then exports this variable to every endless-go subprocess so a relayed Go
// refusal renders for the same reader that asked the question.
//
// Without it, `endless worktree drop --agent` run by a human would relay a
// human-rendered probe refusal into an agent-rendered one, and `--agent-view`
// would stop showing a human what an agent actually sees — which is the only
// thing that flag is for.
const AudienceVar = "ENDLESS_AUDIENCE"

// AudienceAgent is AudienceVar's only meaningful value. Anything else — unset,
// empty, "human", a typo — means "a person is reading this", which is the safe
// direction: a human shown an agent directive is confused for one line, an
// agent shown none reports a refusal it should have handled.
const AudienceAgent = "agent"

// Agent reports whether an agent is reading this process's stderr.
//
// Two sources, deliberately: the harness environment this process was started
// in (agentenv.Present, E-1962/E-2006 — the same predicate events.Actor uses,
// so a consumer holding an event and a consumer holding a process environment
// reach the same answer), and AudienceVar, which is how the Python half passes
// its own already-made decision down.
//
// Harness detection is NOT widened here. An agent running under a harness
// Endless does not recognise reads as a human, exactly as it does everywhere
// else; the fix for that is a detector row in agentenv backed by a real
// environment dump, not a looser test in one consumer.
func Agent() bool {
	return agentenv.Present() || os.Getenv(AudienceVar) == AudienceAgent
}
