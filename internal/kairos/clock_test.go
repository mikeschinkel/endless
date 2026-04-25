package kairos_test

import (
	"sync"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/kairos"
)

func TestClockNow_Monotonic(t *testing.T) {
	c := kairos.NewClock(0x1234)
	prev := c.Now()
	for i := range 100 {
		next := c.Now()
		if !next.After(prev) {
			t.Fatalf("iteration %d: Now() not monotonically increasing", i)
		}
		prev = next
	}
}

func TestClockNow_LogicalIncrement(t *testing.T) {
	frozen := time.Date(2026, 4, 24, 14, 0, 0, 0, time.UTC)
	c := kairos.NewClockWithNow(0x1234, func() time.Time { return frozen })

	ts0 := c.Now()
	ts1 := c.Now()
	ts2 := c.Now()

	if ts0.Logical() != 0 {
		t.Errorf("first call: logical = %d, want 0", ts0.Logical())
	}
	if ts1.Logical() != 1 {
		t.Errorf("second call: logical = %d, want 1", ts1.Logical())
	}
	if ts2.Logical() != 2 {
		t.Errorf("third call: logical = %d, want 2", ts2.Logical())
	}
	// All should have same physical time
	if !ts0.Physical().Equal(ts1.Physical()) || !ts1.Physical().Equal(ts2.Physical()) {
		t.Error("physical time should be identical for frozen clock")
	}
}

func TestClockNow_PhysicalAdvance(t *testing.T) {
	now := time.Date(2026, 4, 24, 14, 0, 0, 0, time.UTC)
	c := kairos.NewClockWithNow(0x1234, func() time.Time {
		result := now
		now = now.Add(time.Microsecond)
		return result
	})

	ts0 := c.Now()
	ts1 := c.Now()

	if ts0.Logical() != 0 {
		t.Errorf("first: logical = %d, want 0", ts0.Logical())
	}
	if ts1.Logical() != 0 {
		t.Errorf("second: logical = %d, want 0 (physical advanced)", ts1.Logical())
	}
	if !ts1.Physical().After(ts0.Physical()) {
		t.Error("physical time should have advanced")
	}
}

func TestClockNow_WallClockBackward(t *testing.T) {
	now := time.Date(2026, 4, 24, 14, 0, 0, 0, time.UTC)
	calls := 0
	c := kairos.NewClockWithNow(0x1234, func() time.Time {
		calls++
		if calls == 2 {
			return now.Add(-time.Second) // clock goes backward
		}
		return now
	})

	ts0 := c.Now()
	ts1 := c.Now() // wall clock jumped back

	if !ts1.After(ts0) {
		t.Error("timestamp should still be monotonically increasing despite wall clock backward jump")
	}
	// Physical time should NOT go backward
	if ts1.Physical().Before(ts0.Physical()) {
		t.Error("physical time should never decrease")
	}
}

func TestClockReceive_RemoteAhead(t *testing.T) {
	now := time.Date(2026, 4, 24, 14, 0, 0, 0, time.UTC)
	c := kairos.NewClockWithNow(0x1234, func() time.Time { return now })

	remote := kairos.New(now.Add(10*time.Second), 5, 0x5678)
	ts := c.Receive(remote)

	if !ts.Physical().Equal(remote.Physical()) {
		t.Errorf("should adopt remote physical time: got %v, want %v", ts.Physical(), remote.Physical())
	}
	if ts.Logical() != 6 {
		t.Errorf("logical = %d, want 6 (remote.logical + 1)", ts.Logical())
	}
}

func TestClockReceive_RemoteBehind(t *testing.T) {
	now := time.Date(2026, 4, 24, 14, 0, 0, 0, time.UTC)
	c := kairos.NewClockWithNow(0x1234, func() time.Time { return now })

	c.Now() // establish local state

	remote := kairos.New(now.Add(-10*time.Second), 5, 0x5678)
	ts := c.Receive(remote)

	if !ts.Physical().Equal(now.Truncate(time.Microsecond)) {
		t.Errorf("should keep local physical time: got %v", ts.Physical())
	}
}

func TestClockReceive_SamePhysical(t *testing.T) {
	now := time.Date(2026, 4, 24, 14, 0, 0, 0, time.UTC)
	c := kairos.NewClockWithNow(0x1234, func() time.Time { return now })

	c.Now() // logical = 0

	remote := kairos.New(now, 10, 0x5678)
	ts := c.Receive(remote)

	// max(local_logical=0, remote_logical=10) + 1 = 11
	if ts.Logical() != 11 {
		t.Errorf("logical = %d, want 11", ts.Logical())
	}
}

func TestClockConcurrency(t *testing.T) {
	c := kairos.NewClock(0x1234)
	const n = 1000
	var wg sync.WaitGroup
	results := make([]kairos.Timestamp, n)

	wg.Add(n)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			results[idx] = c.Now()
		}(i)
	}
	wg.Wait()

	// Check no duplicates
	seen := make(map[string]bool, n)
	for i, ts := range results {
		s := ts.String()
		if seen[s] {
			t.Fatalf("duplicate timestamp at index %d: %s", i, s)
		}
		seen[s] = true
	}
}

func TestClockOverflow(t *testing.T) {
	frozen := time.Date(2026, 4, 24, 14, 0, 0, 0, time.UTC)
	c := kairos.NewClockWithNow(0x1234, func() time.Time { return frozen })

	var last kairos.Timestamp
	for i := range 200 {
		ts := c.Now()
		if i > 0 && !ts.After(last) {
			t.Fatalf("iteration %d: not monotonically increasing after overflow", i)
		}
		last = ts
	}

	// After 128 calls (0-127), physical should have advanced at least once
	if last.Physical().Equal(frozen.Truncate(time.Microsecond)) {
		t.Error("physical time should have advanced after logical overflow")
	}
}
