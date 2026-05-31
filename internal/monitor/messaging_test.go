package monitor

import (
	"database/sql"
	"testing"
)

// conversationState returns the current state of a conversation row and the
// (possibly NULL) process_b participant. Used to assert state transitions
// driven by ConnectToConversation and CloseConversation.
func conversationState(t *testing.T, db *sql.DB, conversationID string) (state, processB string) {
	t.Helper()
	err := db.QueryRow(
		"SELECT state, COALESCE(process_b, '') FROM conversations WHERE conversation_id=?",
		conversationID,
	).Scan(&state, &processB)
	if err != nil {
		t.Fatalf("read conversation %q: %v", conversationID, err)
	}
	return
}

// TestCreateBeacon_InsertsConversationRow verifies that CreateBeacon produces
// a row in 'beacon' state owned by the caller process, with a non-empty
// conversation_id returned so the peer can connect.
func TestCreateBeacon_InsertsConversationRow(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	id, err := CreateBeacon("proc-a", 1)
	if err != nil {
		t.Fatalf("CreateBeacon: %v", err)
	}
	if id == "" {
		t.Fatal("CreateBeacon returned empty conversation id")
	}

	state, processB := conversationState(t, db, id)
	if state != "beacon" {
		t.Errorf("state = %q, want beacon", state)
	}
	if processB != "" {
		t.Errorf("process_b = %q, want empty (no peer yet)", processB)
	}
	var processA string
	if err := db.QueryRow(
		"SELECT process_a FROM conversations WHERE conversation_id=?", id,
	).Scan(&processA); err != nil {
		t.Fatalf("read process_a: %v", err)
	}
	if processA != "proc-a" {
		t.Errorf("process_a = %q, want proc-a", processA)
	}
}

// TestListBeacons_FiltersByStateAndProject verifies that ListBeacons returns
// only rows in 'beacon' state and, when projectID>0, scopes to that project.
func TestListBeacons_FiltersByStateAndProject(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	seedProject(t, db, 2, "proj-test-2", "/tmp/proj-test-2")

	idP1, err := CreateBeacon("proc-a", 1)
	if err != nil {
		t.Fatalf("create beacon p1: %v", err)
	}
	if _, err := CreateBeacon("proc-b", 2); err != nil {
		t.Fatalf("create beacon p2: %v", err)
	}
	// Add a closed beacon in project 1 — must be excluded.
	closedID, err := CreateBeacon("proc-c", 1)
	if err != nil {
		t.Fatalf("create beacon for close: %v", err)
	}
	if err := CloseConversation(closedID); err != nil {
		t.Fatalf("close: %v", err)
	}

	got, err := ListBeacons(1)
	if err != nil {
		t.Fatalf("ListBeacons(1): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d beacons for project 1, want 1: %+v", len(got), got)
	}
	if got[0].ConversationID != idP1 {
		t.Errorf("ConversationID = %q, want %q", got[0].ConversationID, idP1)
	}

	all, err := ListBeacons(0)
	if err != nil {
		t.Fatalf("ListBeacons(0): %v", err)
	}
	if len(all) != 2 {
		t.Errorf("got %d beacons across all projects, want 2", len(all))
	}
}

// TestConnectToConversation_TransitionsBeaconToConnected verifies that the
// connect step flips state to 'connected' and records process_b.
func TestConnectToConversation_TransitionsBeaconToConnected(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")

	id, err := CreateBeacon("proc-a", 1)
	if err != nil {
		t.Fatalf("create beacon: %v", err)
	}
	info, err := ConnectToConversation(id, "proc-b")
	if err != nil {
		t.Fatalf("ConnectToConversation: %v", err)
	}
	if info.State != "connected" {
		t.Errorf("returned state = %q, want connected", info.State)
	}
	if info.ProcessB != "proc-b" {
		t.Errorf("returned process_b = %q, want proc-b", info.ProcessB)
	}

	state, processB := conversationState(t, db, id)
	if state != "connected" {
		t.Errorf("db state = %q, want connected", state)
	}
	if processB != "proc-b" {
		t.Errorf("db process_b = %q, want proc-b", processB)
	}
}

// TestConnectToConversation_UnknownConversationErrors verifies that connecting
// to a non-existent conversation id returns an error rather than silently
// creating one.
func TestConnectToConversation_UnknownConversationErrors(t *testing.T) {
	withTestDB(t)
	if _, err := ConnectToConversation("does-not-exist", "proc-b"); err == nil {
		t.Fatal("ConnectToConversation on unknown id returned nil, want error")
	}
}

// TestSendMessage_InsertsQueuedRow verifies that SendMessage writes a row in
// 'queued' status with the right sender and body and returns the inserted id.
func TestSendMessage_InsertsQueuedRow(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	convID, err := CreateBeacon("proc-a", 1)
	if err != nil {
		t.Fatalf("create beacon: %v", err)
	}
	if _, err := ConnectToConversation(convID, "proc-b"); err != nil {
		t.Fatalf("connect: %v", err)
	}

	msgID, err := SendMessage(convID, "proc-a", "hello")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if msgID <= 0 {
		t.Errorf("SendMessage returned id %d, want > 0", msgID)
	}

	var sender, body, status string
	if err := db.QueryRow(
		"SELECT sender, body, status FROM messages WHERE id=?", msgID,
	).Scan(&sender, &body, &status); err != nil {
		t.Fatalf("read message: %v", err)
	}
	if sender != "proc-a" {
		t.Errorf("sender = %q, want proc-a", sender)
	}
	if body != "hello" {
		t.Errorf("body = %q, want hello", body)
	}
	if status != "queued" {
		t.Errorf("status = %q, want queued", status)
	}
}

// TestHasPendingMessages_TrueWhenPeerHasQueued verifies the boolean check
// returns true once the peer has sent a message and the conversation is
// connected. The recipient (current process) sees messages whose sender is
// the OTHER party.
func TestHasPendingMessages_TrueWhenPeerHasQueued(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	convID, err := CreateBeacon("proc-a", 1)
	if err != nil {
		t.Fatalf("create beacon: %v", err)
	}
	if _, err := ConnectToConversation(convID, "proc-b"); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// No messages yet → false for both parties.
	has, err := HasPendingMessages("proc-b")
	if err != nil {
		t.Fatalf("HasPendingMessages: %v", err)
	}
	if has {
		t.Error("HasPendingMessages('proc-b') = true with no messages, want false")
	}

	// proc-a sends → proc-b sees pending; proc-a does not (won't read its own).
	if _, err := SendMessage(convID, "proc-a", "ping"); err != nil {
		t.Fatalf("send: %v", err)
	}
	has, err = HasPendingMessages("proc-b")
	if err != nil {
		t.Fatalf("HasPendingMessages: %v", err)
	}
	if !has {
		t.Error("HasPendingMessages('proc-b') = false after peer sent, want true")
	}
	has, err = HasPendingMessages("proc-a")
	if err != nil {
		t.Fatalf("HasPendingMessages: %v", err)
	}
	if has {
		t.Error("HasPendingMessages('proc-a') = true for sender's own message, want false")
	}
	_ = db
}

// TestGetPendingMessages_ReturnsAndMarksDelivered verifies the read-and-mark
// side effect: a second call returns no rows because the first call flipped
// status to 'delivered'.
func TestGetPendingMessages_ReturnsAndMarksDelivered(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	convID, err := CreateBeacon("proc-a", 1)
	if err != nil {
		t.Fatalf("create beacon: %v", err)
	}
	if _, err := ConnectToConversation(convID, "proc-b"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := SendMessage(convID, "proc-a", "one"); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	if _, err := SendMessage(convID, "proc-a", "two"); err != nil {
		t.Fatalf("send 2: %v", err)
	}

	got, err := GetPendingMessages("proc-b")
	if err != nil {
		t.Fatalf("GetPendingMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages on first call, want 2", len(got))
	}
	if got[0].Body != "one" || got[1].Body != "two" {
		t.Errorf("delivery order wrong: got %q, %q; want one, two", got[0].Body, got[1].Body)
	}

	// Side effect: second call returns nothing — the rows are now 'delivered'.
	again, err := GetPendingMessages("proc-b")
	if err != nil {
		t.Fatalf("GetPendingMessages 2: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("got %d messages on second call, want 0 (delivered side-effect missing)", len(again))
	}

	// Confirm the rows' status flipped in storage.
	var delivered int
	if err := db.QueryRow(
		"SELECT count(*) FROM messages WHERE conversation_id=? AND status='delivered'",
		convID,
	).Scan(&delivered); err != nil {
		t.Fatalf("count delivered: %v", err)
	}
	if delivered != 2 {
		t.Errorf("delivered rows = %d, want 2", delivered)
	}
}

// TestGetTargetProcess_ReturnsOtherParty verifies the lookup returns whichever
// participant is NOT the caller — used so a send-side helper can address the
// peer by name without round-tripping the conversation row.
func TestGetTargetProcess_ReturnsOtherParty(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	convID, err := CreateBeacon("proc-a", 1)
	if err != nil {
		t.Fatalf("create beacon: %v", err)
	}
	if _, err := ConnectToConversation(convID, "proc-b"); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if got, err := GetTargetProcess(convID, "proc-a"); err != nil || got != "proc-b" {
		t.Errorf("GetTargetProcess(.., proc-a) = (%q, %v), want (proc-b, nil)", got, err)
	}
	if got, err := GetTargetProcess(convID, "proc-b"); err != nil || got != "proc-a" {
		t.Errorf("GetTargetProcess(.., proc-b) = (%q, %v), want (proc-a, nil)", got, err)
	}
	_ = db
}

// TestGetTargetProcess_NotConnectedErrors verifies that an un-connected
// conversation (state='beacon') is rejected — there's no peer to address yet.
func TestGetTargetProcess_NotConnectedErrors(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	convID, err := CreateBeacon("proc-a", 1)
	if err != nil {
		t.Fatalf("create beacon: %v", err)
	}
	if _, err := GetTargetProcess(convID, "proc-a"); err == nil {
		t.Error("GetTargetProcess on beacon-only conversation returned nil, want error")
	}
	_ = db
}

// TestCloseConversation_MarksClosed verifies state transitions to 'closed' and
// closed_at is populated.
func TestCloseConversation_MarksClosed(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	convID, err := CreateBeacon("proc-a", 1)
	if err != nil {
		t.Fatalf("create beacon: %v", err)
	}
	if err := CloseConversation(convID); err != nil {
		t.Fatalf("CloseConversation: %v", err)
	}
	state, _ := conversationState(t, db, convID)
	if state != "closed" {
		t.Errorf("state = %q, want closed", state)
	}
	var closedAt sql.NullString
	if err := db.QueryRow(
		"SELECT closed_at FROM conversations WHERE conversation_id=?", convID,
	).Scan(&closedAt); err != nil {
		t.Fatalf("read closed_at: %v", err)
	}
	if !closedAt.Valid || closedAt.String == "" {
		t.Errorf("closed_at = %v, want non-empty timestamp", closedAt)
	}
}
