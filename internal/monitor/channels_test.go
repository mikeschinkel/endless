package monitor

import (
	"testing"
)

// TestRegisterChannelPort_InsertsRow covers the happy path: a brand-new
// (process, port, pid) tuple is inserted and round-trips through
// LookupChannelPort. This is the MCP channel-plugin handshake — at
// SessionStart the plugin opens a port and writes it here so other
// session-aware tools can dial back.
func TestRegisterChannelPort_InsertsRow(t *testing.T) {
	withTestDB(t)

	if err := RegisterChannelPort("%5", 9001, 42); err != nil {
		t.Fatalf("register: %v", err)
	}
	port, pid, err := LookupChannelPort("%5")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if port != 9001 {
		t.Errorf("port = %d, want 9001", port)
	}
	if pid != 42 {
		t.Errorf("pid = %d, want 42", pid)
	}
}

// TestRegisterChannelPort_UpsertOverwrites pins the ON CONFLICT branch:
// a re-registration for the same process row updates port and pid in
// place rather than failing the UNIQUE constraint. This is the
// channel-plugin restart scenario — same pane, new port.
func TestRegisterChannelPort_UpsertOverwrites(t *testing.T) {
	withTestDB(t)

	if err := RegisterChannelPort("%5", 9001, 42); err != nil {
		t.Fatalf("register 1: %v", err)
	}
	if err := RegisterChannelPort("%5", 9002, 99); err != nil {
		t.Fatalf("register 2: %v", err)
	}
	port, pid, err := LookupChannelPort("%5")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if port != 9002 {
		t.Errorf("port = %d, want 9002 (upsert did not overwrite)", port)
	}
	if pid != 99 {
		t.Errorf("pid = %d, want 99 (upsert did not overwrite)", pid)
	}
}

// TestUnregisterChannelPort_DeletesRow pins the DELETE: after
// unregistration, LookupChannelPort returns sql.ErrNoRows so the caller
// knows the channel is gone. The plugin runs this on shutdown.
func TestUnregisterChannelPort_DeletesRow(t *testing.T) {
	withTestDB(t)

	if err := RegisterChannelPort("%5", 9001, 42); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := UnregisterChannelPort("%5"); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if _, _, err := LookupChannelPort("%5"); err == nil {
		t.Error("LookupChannelPort after unregister returned nil error, want sql.ErrNoRows")
	}
}

// TestUnregisterChannelPort_MissingRowNoError: deleting a row that
// never existed is not an error — DELETE matches zero rows and returns
// cleanly. The plugin shutdown path can call this idempotently.
func TestUnregisterChannelPort_MissingRowNoError(t *testing.T) {
	withTestDB(t)

	if err := UnregisterChannelPort("%missing"); err != nil {
		t.Errorf("unregister missing: %v (should be nil)", err)
	}
}

// TestLookupChannelPort_MissingRowErrors: a lookup against an unknown
// process surfaces the underlying sql.ErrNoRows so the caller can
// distinguish "no channel registered" from a successful (0, 0) result.
func TestLookupChannelPort_MissingRowErrors(t *testing.T) {
	withTestDB(t)

	port, pid, err := LookupChannelPort("%missing")
	if err == nil {
		t.Errorf("LookupChannelPort on missing row returned (%d, %d, nil), want error", port, pid)
	}
	if port != 0 || pid != 0 {
		t.Errorf("LookupChannelPort on missing row returned (port=%d, pid=%d), want zeros", port, pid)
	}
}
