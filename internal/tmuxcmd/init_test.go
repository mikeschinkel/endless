package tmuxcmd

import (
	"regexp"
	"testing"
)

// TestNewUUID_ShapeAndUniqueness pins the UUID emitter used by
// `endless tmux init` as the @server_uuid stamp: 8-4-4-4-12 lowercase
// hex, version-4 nibble at position 14 (`...-4xxx-...`), variant nibble
// in {8,9,a,b}. The uniqueness check rules out a constant-output bug.
func TestNewUUID_ShapeAndUniqueness(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := make(map[string]struct{}, 32)
	for i := 0; i < 32; i++ {
		u, err := newUUID()
		if err != nil {
			t.Fatalf("newUUID: %v", err)
		}
		if !pattern.MatchString(u) {
			t.Fatalf("uuid %q does not match v4 8-4-4-4-12 shape", u)
		}
		if _, dup := seen[u]; dup {
			t.Fatalf("uuid collision after %d draws: %q", i, u)
		}
		seen[u] = struct{}{}
	}
}
