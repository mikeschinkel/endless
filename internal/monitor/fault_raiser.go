package monitor

import (
	"database/sql"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/faults"
)

// RaiserEnv is what a process knows about who it is working for, gathered by
// cmd/endless-go so ResolveFaultRaiser decides from values rather than from the
// live process (and can be tested that way).
type RaiserEnv struct {
	// HookSession is the Claude session id of the hook event this process is
	// handling, "" when it is not a hook. A hook is an agent's by construction.
	HookSession string

	// Agent is whether an agent harness runs this process — the same
	// events.DetectedHarness that stamps an event's actor.
	Agent bool

	// ClaudeSession and EndlessSession are CLAUDE_CODE_SESSION_ID and
	// ENDLESS_SESSION_ID as the process inherited them.
	ClaudeSession  string
	EndlessSession string

	// Cwd is the process's working directory.
	Cwd string
}

// ResolveFaultRaiser decides which task and session raised a fault (E-2268),
// filling only the fields the producer left 0.
//
// The session is recorded only when an AGENT ran the process: a hook (named by
// its payload) or a command run under an agent harness (named by Claude Code's
// own session id, else by ENDLESS_SESSION_ID). A person's shell gets no session
// even when one could be inferred from it — `esu` exports ENDLESS_SESSION_ID
// into the user's own shell — because the session is what a fault gets routed
// to, and a fault a person raised must never be routed to an agent.
//
// The task is the producer's, else the session's, else the task whose worktree
// the process runs in. The worktree answer holds for a person too: a command run
// inside .endless/worktrees/e-NNNN is working on E-NNNN whoever typed it.
//
// Read-only and never failing: it runs inside faults.Record, so every lookup
// that errors leaves its field 0, and no lookup may mint a row —
// EnsureClaudeSessionID's lazy insert would make a diagnostic a side effect.
func ResolveFaultRaiser(explicit faults.Raiser, env RaiserEnv) (raiser faults.Raiser) {
	var sessionTask int64

	raiser = explicit
	if raiser.SessionID != 0 {
		_, sessionTask = sessionRaiser("", raiser.SessionID)
	} else {
		raiser.SessionID, sessionTask = agentSession(env)
	}
	if raiser.TaskID == 0 {
		raiser.TaskID = sessionTask
	}
	if raiser.TaskID == 0 {
		raiser.TaskID = worktreeTask(env.Cwd)
	}
	return raiser
}

// agentSession returns the Endless session — and its task — of the agent
// running the process, or zeros when no agent is.
//
// Claude Code's own session id comes before ENDLESS_SESSION_ID: it is the
// harness saying which session this is, where the other is a value `esu`
// exported and a shell can carry stale.
func agentSession(env RaiserEnv) (sessionID, taskID int64) {
	guid := env.HookSession
	if guid == "" {
		if !env.Agent {
			return 0, 0
		}
		guid = env.ClaudeSession
	}
	if guid != "" {
		if sessionID, taskID = sessionRaiser(guid, 0); sessionID != 0 {
			return sessionID, taskID
		}
	}
	if id, err := strconv.ParseInt(env.EndlessSession, 10, 64); err == nil && id > 0 {
		return sessionRaiser("", id)
	}
	return 0, 0
}

// sessionRaiser returns the sessions.id and task_id of the session named by its
// Claude session id (guid), or by its sessions.id when guid is "". Zeros when
// there is no such session or it cannot be read.
func sessionRaiser(guid string, id int64) (sessionID, taskID int64) {
	db, err := DB()
	if err != nil {
		return 0, 0
	}
	var task sql.NullInt64
	query, arg := `SELECT id, task_id FROM sessions WHERE id = ?`, any(id)
	if guid != "" {
		query, arg = `SELECT id, task_id FROM sessions WHERE session_id = ?`, guid
	}
	// One QueryRow, scanned. A *sql.Row holds its connection until Scan, so
	// building one and discarding it un-scanned deadlocks a single-connection
	// pool — which is what every Endless database handle is.
	if db.QueryRow(query, arg).Scan(&sessionID, &task) != nil {
		return 0, 0
	}
	return sessionID, task.Int64
}

// worktreeTask returns the task whose worktree dir lies in, or 0.
func worktreeTask(dir string) (taskID int64) {
	ref := TaskIDFromWorktreePath(dir)
	taskID, _ = strconv.ParseInt(strings.TrimPrefix(ref, "E-"), 10, 64)
	return taskID
}
