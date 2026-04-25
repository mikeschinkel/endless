// Package kairos provides a compact, lexicographically-sortable timestamp
// format backed by Hybrid Logical Clocks (HLC). Kairos timestamps encode
// physical time, a node identifier, and a logical counter into a 15-character
// Crockford Base32 string that sorts in causal order.
package kairos

import (
	"fmt"
	"time"
)

// Epoch is the base time for all Kairos timestamps.
// Chosen to predate any realistic task history (JIRA launched 2002).
// Covers until approximately 2142.
var Epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// Timestamp is a Hybrid Logical Clock value: physical time + logical counter + node ID.
// The zero value represents "no timestamp."
type Timestamp struct {
	physical time.Time // UTC, truncated to microsecond
	logical  uint8     // 0-127 (7 bits)
	nodeID   uint16    // 0-65535 (16 bits)
}

// New creates a Timestamp from its components.
func New(physical time.Time, logical uint8, nodeID uint16) Timestamp {
	if logical > 127 {
		logical = 127
	}
	return Timestamp{
		physical: physical.UTC().Truncate(time.Microsecond),
		logical:  logical,
		nodeID:   nodeID,
	}
}

// Parse decodes a 15-character Crockford Base32 string into a Timestamp.
func Parse(s string) (Timestamp, error) {
	if len(s) != 15 {
		return Timestamp{}, fmt.Errorf("kairos: invalid length %d, want 15", len(s))
	}
	buf, err := decode(s)
	if err != nil {
		return Timestamp{}, fmt.Errorf("kairos: %w", err)
	}
	return unpack(buf), nil
}

// String encodes the Timestamp as a 15-character Crockford Base32 string.
func (t Timestamp) String() string {
	return encode(pack(t))
}

// Before reports whether t is causally before other.
// Agrees with lexicographic comparison of String() values.
func (t Timestamp) Before(other Timestamp) bool {
	if !t.physical.Equal(other.physical) {
		return t.physical.Before(other.physical)
	}
	if t.logical != other.logical {
		return t.logical < other.logical
	}
	return t.nodeID < other.nodeID
}

// After reports whether t is causally after other.
func (t Timestamp) After(other Timestamp) bool {
	return other.Before(t)
}

// Equal reports whether t and other represent the same timestamp.
func (t Timestamp) Equal(other Timestamp) bool {
	return t.physical.Equal(other.physical) &&
		t.logical == other.logical &&
		t.nodeID == other.nodeID
}

// Physical returns the physical time component (UTC, microsecond precision).
func (t Timestamp) Physical() time.Time { return t.physical }

// Logical returns the logical counter (0-127).
func (t Timestamp) Logical() uint8 { return t.logical }

// NodeID returns the node identifier as a uint16.
func (t Timestamp) NodeID() uint16 { return t.nodeID }

// NodeHex returns the node identifier as a 4-character lowercase hex string.
func (t Timestamp) NodeHex() string { return FormatNodeID(t.nodeID) }

// IsZero reports whether t is the zero Timestamp.
func (t Timestamp) IsZero() bool {
	return t.physical.IsZero() && t.logical == 0 && t.nodeID == 0
}

// Format returns the physical time formatted with the given Go time layout.
func (t Timestamp) Format(layout string) string {
	return t.physical.Format(layout)
}

// Bit layout (75 bits, big-endian, packed into 10 bytes with 5 leading zero bits):
//
//   [5 zero bits] [52-bit µs offset] [7-bit logical] [16-bit node ID]
//
// This order matches Before() comparison (physical → logical → nodeID)
// so that lexicographic string sort agrees with causal ordering.

// pack encodes a Timestamp into 10 bytes (80 bits, top 5 bits zero).
func pack(t Timestamp) [10]byte {
	usec := uint64(t.physical.Sub(Epoch) / time.Microsecond)
	logical := uint64(t.logical & 0x7F)
	node := uint64(t.nodeID)

	// 75-bit value: usec(52) | logical(7) | node(16)
	// Stored right-aligned in 80 bits (5 leading zeros).
	lo := (usec << 23) | (logical << 16) | node
	hi := usec >> 41 // top 11 bits of usec

	var buf [10]byte
	hi16 := uint16(hi)
	buf[0] = byte(hi16 >> 8)
	buf[1] = byte(hi16)
	buf[2] = byte(lo >> 56)
	buf[3] = byte(lo >> 48)
	buf[4] = byte(lo >> 40)
	buf[5] = byte(lo >> 32)
	buf[6] = byte(lo >> 24)
	buf[7] = byte(lo >> 16)
	buf[8] = byte(lo >> 8)
	buf[9] = byte(lo)
	return buf
}

// unpack decodes 10 bytes into a Timestamp.
func unpack(buf [10]byte) Timestamp {
	hi16 := uint64(buf[0])<<8 | uint64(buf[1])
	lo := uint64(buf[2])<<56 | uint64(buf[3])<<48 |
		uint64(buf[4])<<40 | uint64(buf[5])<<32 |
		uint64(buf[6])<<24 | uint64(buf[7])<<16 |
		uint64(buf[8])<<8 | uint64(buf[9])

	node := uint16(lo & 0xFFFF)
	logical := uint8((lo >> 16) & 0x7F)
	usec := (hi16 << 41) | (lo >> 23)

	physical := Epoch.Add(time.Duration(usec) * time.Microsecond)
	return Timestamp{
		physical: physical,
		logical:  logical,
		nodeID:   node,
	}
}
