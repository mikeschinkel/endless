package monitor

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// server_aliases.go is the ONE same-server test for tmux server ids (E-2428).
//
// A tmux server keeps its @server_uuid for its lifetime, except when one is
// stamped over it. On 2026-10-19 a failed read re-stamped a live server, so it
// carried a second id, and every session bound under the first read dead: since
// E-2300 a different id on the same socket proves the old server gone. The
// re-stamp is fixed at its source (TmuxServerUUID, `tmux init`), but an id the
// running server has already carried must keep naming it. tmux_server_aliases
// records those earlier ids, and every comparison of two server ids goes
// through SameTmuxServerSQL or SameTmuxServer, which accept an id or any alias
// of it.

// tmuxServerSQL is the SQL expression naming the server the id in expr belongs
// to now: the id it is an alias of, else the id itself. One hop is enough,
// because recordTmuxServerAliases points every alias at the newest id.
func tmuxServerSQL(expr string) string {
	return `ifnull((SELECT sa.server_uuid FROM tmux_server_aliases sa
	                 WHERE sa.alias_uuid = ` + expr + `), ` + expr + `)`
}

// SameTmuxServerSQL is the SQL condition true when the server ids a and b name
// the same tmux server: equal, or one an alias of the other's server.
//
// a and b are table-qualified columns (`p.server_uuid`) or NUMBERED bind
// parameters (`?2`). Each is spelled more than once in the result, so a bare `?`
// would bind twice; and each is spelled inside a subquery on
// tmux_server_aliases, so an unqualified `server_uuid` would name that table's
// column instead of the caller's. ifnull() on both sides for the
// session_liveness view's reason: a NULL server_uuid is legitimate for kind=pid
// and E-1898's backfill, and SQL equality with NULL is never true.
func SameTmuxServerSQL(a, b string) string {
	return `ifnull(` + tmuxServerSQL(a) + `, '') = ifnull(` + tmuxServerSQL(b) + `, '')`
}

// rowQuerier is the one method SameTmuxServer needs, so it runs on a *sql.DB or
// inside a caller's open *sql.Tx alike.
type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// SameTmuxServer is SameTmuxServerSQL for two ids already in hand.
func SameTmuxServer(q rowQuerier, a, b string) (bool, error) {
	var same bool
	if err := q.QueryRow(`SELECT `+SameTmuxServerSQL("?1", "?2"), a, b).Scan(&same); err != nil {
		return false, fmt.Errorf("same tmux server %q %q: %w", a, b, err)
	}
	return same, nil
}

// tmuxStartTimeFn is the seam tests swap for a fixed server start time.
// Production always uses liveTmuxStartTime.
var tmuxStartTimeFn = liveTmuxStartTime

// tmuxStartTimeArgs builds `tmux display-message -p #{start_time}`: when the
// running server process started, in Unix seconds.
func tmuxStartTimeArgs() []string {
	return []string{"display-message", "-p", "#{start_time}"}
}

// liveTmuxStartTime returns when the ambient tmux server started, or the zero
// time when it cannot be read.
func liveTmuxStartTime() time.Time {
	out, err := tmuxCommand(tmuxStartTimeArgs()...).Output()
	if err != nil {
		return time.Time{}
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || secs <= 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0)
}

// recordTmuxServerAliases records, for uuid just recorded on socket, every
// other recorded id on that socket first seen at or after the running server's
// start: those can only have been stamped on this same server process, so each
// becomes an alias of uuid. An id first seen before the start belonged to an
// earlier server — tmux reuses pane ids across restarts, so it must stay a
// different identity — and gets no alias.
//
// Both times come from this machine's clock. REPLACE re-points an id that was
// already an alias of an earlier one, so every alias names the newest id and
// tmuxServerSQL needs one hop. With no start time there is no evidence, and
// nothing is recorded.
func recordTmuxServerAliases(db *sql.DB, uuid, socket string, started time.Time) {
	if started.IsZero() {
		return
	}
	_, _ = db.Exec(
		`INSERT OR REPLACE INTO tmux_server_aliases (alias_uuid, server_uuid, recorded_at)
		 SELECT ts.server_uuid, ?1, strftime('%Y-%m-%dT%H:%M:%S', 'now')
		   FROM tmux_servers ts
		  WHERE ts.socket_path = ?2 AND ts.server_uuid != ?1 AND ts.first_seen_at >= ?3`,
		uuid, socket, started.UTC().Format("2006-01-02T15:04:05"),
	)
}
