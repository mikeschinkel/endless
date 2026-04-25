package kairos

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// GenerateNodeID creates a random 16-bit node identifier using crypto/rand.
func GenerateNodeID() uint16 {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("kairos: crypto/rand failed: " + err.Error())
	}
	return uint16(b[0])<<8 | uint16(b[1])
}

// FormatNodeID formats a node ID as a 4-character lowercase hex string.
func FormatNodeID(id uint16) string {
	return fmt.Sprintf("%04x", id)
}

// ParseNodeID parses a 4-character hex string into a node ID.
func ParseNodeID(s string) (uint16, error) {
	s = strings.TrimSpace(s)
	if len(s) != 4 {
		return 0, fmt.Errorf("kairos: node ID must be 4 hex chars, got %d", len(s))
	}
	var id uint16
	for _, c := range s {
		id <<= 4
		switch {
		case c >= '0' && c <= '9':
			id |= uint16(c - '0')
		case c >= 'a' && c <= 'f':
			id |= uint16(c-'a') + 10
		case c >= 'A' && c <= 'F':
			id |= uint16(c-'A') + 10
		default:
			return 0, fmt.Errorf("kairos: invalid hex character %q in node ID", c)
		}
	}
	return id, nil
}
