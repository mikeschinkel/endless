package kairos

import (
	"sync"
	"time"
)

// Clock holds HLC state and produces causally-ordered timestamps.
// It is safe for concurrent use.
type Clock struct {
	mu      sync.Mutex
	last    Timestamp
	nodeID  uint16
	nowFunc func() time.Time
}

// NewClock creates a Clock with the given node ID.
func NewClock(nodeID uint16) *Clock {
	return &Clock{
		nodeID:  nodeID,
		nowFunc: time.Now,
	}
}

// NewClockWithNow creates a Clock with an injectable time source (for testing).
func NewClockWithNow(nodeID uint16, nowFunc func() time.Time) *Clock {
	return &Clock{
		nodeID:  nodeID,
		nowFunc: nowFunc,
	}
}

// Now produces a new timestamp for a local event.
// The returned timestamp is guaranteed to be After the previous one from this Clock.
func (c *Clock) Now() Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()

	wall := c.nowFunc().UTC().Truncate(time.Microsecond)

	var phys time.Time
	if wall.After(c.last.physical) {
		phys = wall
	} else {
		phys = c.last.physical
	}

	var logical uint8
	if phys.Equal(c.last.physical) {
		logical = c.last.logical + 1
	}

	// Overflow: advance physical time by 1µs, reset counter
	if logical > 127 {
		phys = phys.Add(time.Microsecond)
		logical = 0
	}

	c.last = Timestamp{
		physical: phys,
		logical:  logical,
		nodeID:   c.nodeID,
	}
	return c.last
}

// Receive produces a new timestamp incorporating a remote timestamp.
// This advances the local clock to maintain causality with the remote event.
func (c *Clock) Receive(remote Timestamp) Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()

	wall := c.nowFunc().UTC().Truncate(time.Microsecond)

	// phys = max(wall, last.physical, remote.physical)
	phys := wall
	if c.last.physical.After(phys) {
		phys = c.last.physical
	}
	if remote.physical.After(phys) {
		phys = remote.physical
	}

	var logical uint8
	switch {
	case phys.Equal(c.last.physical) && phys.Equal(remote.physical):
		// All three equal: take max of both logical counters + 1
		l := c.last.logical
		if remote.logical > l {
			l = remote.logical
		}
		logical = l + 1
	case phys.Equal(c.last.physical):
		logical = c.last.logical + 1
	case phys.Equal(remote.physical):
		logical = remote.logical + 1
	default:
		logical = 0
	}

	if logical > 127 {
		phys = phys.Add(time.Microsecond)
		logical = 0
	}

	c.last = Timestamp{
		physical: phys,
		logical:  logical,
		nodeID:   c.nodeID,
	}
	return c.last
}

// NodeID returns the clock's node identifier.
func (c *Clock) NodeID() uint16 { return c.nodeID }
