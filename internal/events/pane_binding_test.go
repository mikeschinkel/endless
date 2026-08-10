package events

import (
	"database/sql"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// seedPane returns the processes.id for a tmux pane, creating the row, and pins
// the tmux server identity for the duration of the test (E-1898).
//
// Both halves matter. Fixtures used to write a bare pane string into
// sessions.process; a binding is now (server_uuid, address), so the row has to
// exist and the code under test has to agree about which server it is on.
// Pinning also keeps `go test` off the developer's real tmux: run from inside a
// pane, the resolution path would otherwise stamp @server_uuid on the live
// server and resolve fixtures against real panes.
func seedPane(t *testing.T, db *sql.DB, pane string) int64 {
	t.Helper()
	t.Cleanup(monitor.SetTestTmuxServer(monitor.TestServerUUID))
	id, err := monitor.SeedPaneProcess(db, monitor.TestServerUUID, pane)
	if err != nil {
		t.Fatalf("seed pane binding %q: %v", pane, err)
	}
	return id
}
