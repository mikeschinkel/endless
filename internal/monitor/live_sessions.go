package monitor

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/tmuxservers"
)

// LiveSession is the row shape returned by ListLiveSessions, intended for
// JSON serialization to Python callers. PaneID is set to Process when
// Process is a tmux pane id ("%<digits>") bound on the tmux server THIS
// process reaches, else nil — so the Python side can distinguish tmux from
// non-tmux sessions without re-parsing the process string, and can compare
// PaneID with $TMUX_PANE or hand it to a tmux command without asking which
// server it belongs to.
//
// The server scoping is E-2196. A pane id is only unique per server: a tmux
// restart reissues "%45" to an unrelated pane, and a session from before the
// crash (liveness `unknown` — its server is gone, not observed) stays listed
// as live. Reporting its bare "%45" let every Python caller that matched
// pane ids by string treat the restored pane as that old session's — which is
// how `session resume E-2135`, typed into a restored window, refused with
// "This pane is working E-2105". GetLiveSessionByProcess and
// ProcessIDsForPanes already scope by server for exactly this reason; this
// is the same rule at the row Python reads. Process still carries the raw
// address, and the row itself is kept: the claim is unaffected.
type LiveSession struct {
	SessionID        string  `json:"session_id"`
	EndlessSessionID int64   `json:"endless_session_id"`
	ProjectID        int64   `json:"project_id"`
	Platform         string  `json:"platform"`
	State            string  `json:"state"`
	TaskID           *int64  `json:"task_id"`
	Process          string  `json:"process"`
	PaneID           *string `json:"pane_id"`
	StartedAt        string  `json:"started_at"`
	LastActivity     string  `json:"last_activity"`
	// Liveness is the observed run state (E-1898): "live" (pane present on a
	// server we reached), "unknown" (server unreachable — no opinion), or
	// "unbound" (no pane binding at all). "dead" never
	// appears: ListLiveSessions filters those out. Exposed so a caller can tell
	// a proven-live owner from one we merely could not disprove, rather than
	// inferring it from the row's presence.
	Liveness string `json:"liveness"`
	// EndlessServerDown is set on an `unknown` session whose recorded socket is
	// one of Endless's own tmux servers (E-2240) and nothing answers there, so
	// `endless start` is what brings its server back (E-2300). False whenever
	// that cannot be shown, including every session bound before E-2300.
	EndlessServerDown bool `json:"endless_server_down"`
}

// presentSession is the ONE SQL predicate (over sessions s and session_liveness
// sl) for "this session is still here": recorded live, and not observably gone.
// The claim guard (ListLiveSessions), stewardship, WorktreeInUse, session
// status's in_flight marker and the session graph all use it (E-2300), so a
// session cannot be gone to one of them and present to another. `unknown`
// qualifies — unprovable is not gone — and since E-2300 a session whose server
// is proven gone reads `dead`, so it does not.
var presentSession = `s.state IN (` + liveSessionStates + `) AND sl.liveness != 'dead'`

// ListLiveSessions returns the non-ended sessions for projectID that are not
// OBSERVABLY gone, ordered by most-recent activity first. Replaces the
// Python-side `_read_live_companions` glob-and-filter pattern (E-1426).
//
// Two filters, and the difference between them is the point (E-1898):
//
//   - sessionstate.Live — a recorded FACT. Something reported the session over.
//   - liveness != 'dead' — a fresh OBSERVATION. We reached the session's tmux
//     server and its pane was not there.
//
// This is where the dead-pane reaper's job went. The spawn/claim ownership
// guard used to call a reaper first so a ghost owner would be WRITTEN to
// 'ended' before it read ownership; now the ghost simply does not appear in
// this list, and nothing was written to make that true.
//
// Sessions whose server could not be reached read 'unknown' and DELIBERATELY
// remain in the list. They are still live as far as anyone can prove, so the
// guard keeps refusing. A wrong refusal costs a retry; the inverse hands a live
// worktree to a second session.
func ListLiveSessions(projectID int64) ([]LiveSession, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	if err = livenessReady(); err != nil {
		return nil, err
	}
	// An unidentifiable server (no tmux, or none reachable) yields "", which
	// no binding matches: an unscoped pane id is exactly what must not leak.
	here, _ := TmuxServerUUID()

	// Numbered placeholders: SameTmuxServerSQL spells ?2 twice.
	rows, err := db.Query(
		`SELECT s.session_id, s.id, COALESCE(s.project_id, 0), s.platform, s.state,
		        s.task_id, COALESCE(p.address, ''),
		        COALESCE(s.started_at, ''), COALESCE(s.last_activity, ''),
		        sl.liveness, COALESCE(ts.socket_path, ''),
		        ?2 != '' AND `+SameTmuxServerSQL("p.server_uuid", "?2")+`
		 FROM sessions s
		 LEFT JOIN processes p ON p.id = s.process_id
		 LEFT JOIN tmux_servers ts ON ts.server_uuid = `+tmuxServerSQL("p.server_uuid")+`
		 JOIN session_liveness sl ON sl.session_id = s.id
		 WHERE `+presentSession+` AND s.project_id = ?1
		 ORDER BY s.last_activity DESC`,
		projectID, here,
	)
	if err != nil {
		return nil, fmt.Errorf("query live sessions: %w", err)
	}
	defer rows.Close()

	out := []LiveSession{}
	down := map[string]bool{}
	for rows.Next() {
		var s LiveSession
		var socket string
		var onHere bool
		if err := rows.Scan(
			&s.SessionID, &s.EndlessSessionID, &s.ProjectID, &s.Platform, &s.State,
			&s.TaskID, &s.Process, &s.StartedAt, &s.LastActivity,
			&s.Liveness, &socket, &onHere,
		); err != nil {
			return nil, fmt.Errorf("scan live session: %w", err)
		}
		if s.Liveness == string(LivenessUnknown) && socket != "" {
			d, ok := down[socket]
			if !ok {
				d = endlessServerDown(socket)
				down[socket] = d
			}
			s.EndlessServerDown = d
		}
		if strings.HasPrefix(s.Process, "%") && onHere {
			p := s.Process
			s.PaneID = &p
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate live sessions: %w", err)
	}
	return out, nil
}

// endlessServerDown reports whether socket is one of Endless's own tmux servers
// and nothing answers on it. A seam: setTestTmux stubs it, so no test asks a
// real Endless server.
var endlessServerDown = liveEndlessServerDown

func liveEndlessServerDown(socket string) bool {
	servers, err := tmuxservers.Resolve()
	if err != nil {
		return false
	}
	for _, srv := range []tmuxservers.Server{servers.Console, servers.Tasks} {
		if sameSocketPath(srv.Socket, socket) {
			return !srv.Reachable()
		}
	}
	return false
}

// sameSocketPath compares two socket paths through a symlinked directory
// (/tmp is /private/tmp on macOS). The socket itself may be gone, so only its
// directory is resolved.
func sameSocketPath(a, b string) bool {
	canon := func(p string) string {
		p = filepath.Clean(p)
		if dir, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
			return filepath.Join(dir, filepath.Base(p))
		}
		return p
	}
	return canon(a) == canon(b)
}
