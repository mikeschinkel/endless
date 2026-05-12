package events

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestActor_JSON_SessionIDOmitWhenEmpty verifies the omitempty tag works:
// pre-E-1284 events (with no session_id) keep their existing JSON shape,
// and new events with a session attach the field.
func TestActor_JSON_SessionIDOmitWhenEmpty(t *testing.T) {
	with := Actor{Kind: ActorCLI, ID: "user@host", SessionID: "356"}
	b, err := json.Marshal(with)
	if err != nil {
		t.Fatalf("marshal with session: %v", err)
	}
	if !strings.Contains(string(b), `"session_id":"356"`) {
		t.Errorf("expected session_id in JSON, got %s", string(b))
	}

	without := Actor{Kind: ActorCLI, ID: "user@host"}
	b, err = json.Marshal(without)
	if err != nil {
		t.Fatalf("marshal without session: %v", err)
	}
	if strings.Contains(string(b), "session_id") {
		t.Errorf("expected session_id to be omitted, got %s", string(b))
	}
}

// TestActor_JSON_RoundTrip ensures unmarshaling pre-E-1284 events (no
// session_id field) leaves SessionID empty without erroring — important
// for backwards compatibility with the existing ledger.
func TestActor_JSON_RoundTrip(t *testing.T) {
	legacy := `{"kind":"cli","id":"user@host"}`
	var a Actor
	if err := json.Unmarshal([]byte(legacy), &a); err != nil {
		t.Fatalf("unmarshal legacy actor: %v", err)
	}
	if a.SessionID != "" {
		t.Errorf("expected empty SessionID for legacy actor, got %q", a.SessionID)
	}
	if a.Kind != ActorCLI || a.ID != "user@host" {
		t.Errorf("legacy fields not preserved: %+v", a)
	}

	current := `{"kind":"cli","id":"user@host","session_id":"356"}`
	var a2 Actor
	if err := json.Unmarshal([]byte(current), &a2); err != nil {
		t.Fatalf("unmarshal current actor: %v", err)
	}
	if a2.SessionID != "356" {
		t.Errorf("expected SessionID=356, got %q", a2.SessionID)
	}
}
