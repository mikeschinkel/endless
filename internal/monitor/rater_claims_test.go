package monitor

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

func claimTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err = schema.Migrate(db); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

// The whole point: two claimants, one task, exactly one winner. Without this
// the job sweep and a hand-run `endless rater run` both pay for the same
// model call.
func TestClaimRater_SecondClaimantLoses(t *testing.T) {
	db := claimTestDB(t)

	first, err := claimRater(db, 42, "owner-a", time.Minute)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !first {
		t.Fatal("first claimant did not win an unclaimed task")
	}

	second, err := claimRater(db, 42, "owner-b", time.Minute)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second {
		t.Error("second claimant won a live claim; duplicate model spend")
	}
}

// A claimant that dies mid-call must not wedge the task forever — that is why
// the claim is time-boxed rather than an OS lock.
func TestClaimRater_LapsedClaimIsReclaimable(t *testing.T) {
	db := claimTestDB(t)

	// Zero TTL expires at the same second it is taken, so the next claim sees
	// expires_at <= now and takes it over.
	if _, err := claimRater(db, 7, "dead-owner", 0); err != nil {
		t.Fatalf("seed lapsed claim: %v", err)
	}
	got, err := claimRater(db, 7, "live-owner", time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if !got {
		t.Error("a lapsed claim was not reclaimable; a dead claimant would wedge the task")
	}
}

func TestReleaseRater_FreesTheTask(t *testing.T) {
	db := claimTestDB(t)
	if _, err := claimRater(db, 9, "owner-a", time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := releaseRater(db, 9, "owner-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, err := claimRater(db, 9, "owner-b", time.Minute)
	if err != nil {
		t.Fatalf("re-claim after release: %v", err)
	}
	if !got {
		t.Error("release did not free the task")
	}
}

// Releasing a claim that already lapsed and was retaken must not steal it.
func TestReleaseRater_DoesNotStealAnotherOwnersClaim(t *testing.T) {
	db := claimTestDB(t)
	if _, err := claimRater(db, 11, "owner-a", 0); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := claimRater(db, 11, "owner-b", time.Minute); err != nil {
		t.Fatalf("takeover: %v", err)
	}
	// owner-a, finishing late, releases what it thinks is its claim.
	if err := releaseRater(db, 11, "owner-a"); err != nil {
		t.Fatalf("stale release: %v", err)
	}
	stillHeld, err := claimRater(db, 11, "owner-c", time.Minute)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if stillHeld {
		t.Error("a stale release freed the live owner's claim")
	}
}

func TestRaterClaimOwner_IsStableWithinAProcess(t *testing.T) {
	if RaterClaimOwner() != RaterClaimOwner() {
		t.Error("owner identity must be stable so release matches claim")
	}
	if RaterClaimOwner() == "" {
		t.Error("owner identity is empty")
	}
}
