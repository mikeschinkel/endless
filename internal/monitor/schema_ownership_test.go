package monitor

import "testing"

// TestForeignRealDB pins who may write SCHEMA to the main database (E-1818,
// reopened by E-1975).
//
// The invariant: an unlanded worktree build may write DATA to the real database
// (session and pane state is real-world activity, per E-1450) and may never
// migrate, reseed or fail-close it.
//
// E-1818 enforced that only for the FORCE-PIN path (ForceRealDB / PinMainDB).
// An explicit DB flag left the override empty and sailed straight past it —
// and `endless --db main <anything>` threads exactly that flag to every
// endless-go shellout, so the hole was on the documented daily path rather than
// in some corner. A branch that added a table to schema.sql created it in the
// user's main database the first time an agent ran a routine command.
func TestForeignRealDB(t *testing.T) {
	const (
		real      = "/Users/x/.config/endless/endless.db"
		sandbox   = "/Users/x/.cache/endless/sandboxes/e-1975/endless/endless.db"
		deployed  = "/usr/local/bin/endless-go"
		candidate = "/Users/x/Projects/endless/.endless/worktrees/e-1975/bin/endless-go"
	)
	cases := []struct {
		name     string
		override string
		exe      string
		dbPath   string
		realPath string
		want     bool
	}{
		{
			name:     "force-pinned is foreign, whatever the binary",
			override: real, exe: deployed, dbPath: real, realPath: real, want: true,
		},
		{
			// The case E-1975 found. Explicit DB flag, candidate binary,
			// main database.
			name: "candidate build pointed at the main database by an explicit flag",
			exe:  candidate, dbPath: real, realPath: real, want: true,
		},
		{
			// The deployed binary OWNS the schema. It has to keep applying it,
			// or a landed change would never reach the database.
			name: "deployed build on the main database still owns the schema",
			exe:  deployed, dbPath: real, realPath: real, want: false,
		},
		{
			// A worktree's own sandbox is the one database a candidate build is
			// entitled to migrate freely — that is what it is for.
			name: "candidate build on its own sandbox",
			exe:  candidate, dbPath: sandbox, realPath: real, want: false,
		},
		{
			// Tests and tooling point at throwaway databases constantly. Going
			// passive there would leave them with no schema at all.
			name: "candidate build on a throwaway database",
			exe:  candidate, dbPath: "/tmp/scratch/endless.db", realPath: real, want: false,
		},
		{
			// An unresolvable home falls back to the override answer rather than
			// guessing. Guessing permissively reopens the hole; guessing strictly
			// leaves a fresh install with no schema.
			name: "unknown home falls back to the override",
			exe:  candidate, dbPath: real, realPath: "", want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := foreignRealDB(c.override, c.exe, c.dbPath, c.realPath)
			if got != c.want {
				t.Errorf("foreignRealDB(%q, %q, %q, %q) = %v, want %v",
					c.override, c.exe, c.dbPath, c.realPath, got, c.want)
			}
		})
	}
}

// The marker the decision keys on has to be the one worktrees are actually
// created under, or the check silently never fires.
func TestWorktreePathMarkerMatchesRealLayout(t *testing.T) {
	const exe = "/Users/x/Projects/endless/.endless/worktrees/e-1975/bin/endless-go"
	if !foreignRealDB("", exe, "/db", "/db") {
		t.Errorf("a real worktree binary path did not match %q", worktreePathMarker)
	}
}
