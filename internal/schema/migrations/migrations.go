// Package migrations holds the versioned migration set: the `.sql` steps the
// parent package embeds, and the `.go` steps Go() registers.
//
// Both kinds live in this one directory on purpose. `ls` over it is how a reader
// finds out what the schema has done, in version order, and a Go step filed
// somewhere else would make that listing lie by omission.
//
// WHEN A STEP HAS TO BE `.go`
//
// SQL is the default and most steps stay SQL. A step comes here when SQLite's
// DDL cannot express what the step has to be, and there is one recurring case:
// a DESTRUCTIVE step that must tolerate a database already at the target shape.
// SQLite has DROP TABLE IF EXISTS but no DROP COLUMN IF EXISTS, so a column drop
// cannot be written safely in SQL at all.
//
// That tolerance is required, not a nicety. internal/schema/schema.sql is still
// exec'd directly — by tests, and by anything building a database from the
// declared shape — so a database can reach a migration already holding what that
// migration intends to produce. 00002 met the additive half of this and said so
// in its own header; a Go step is how the destructive half is met.
//
// It is NOT the baseline's blanket idempotence, which 00001 alone may have and
// whose header rightly warns against copying. The baseline no-ops WHOLESALE, so
// a run and a skip are indistinguishable. A step here probes one named object
// and skips only that, for a reason it states — the same narrow, justified
// conditionality `DROP TABLE IF EXISTS` carries, in the one place SQLite gives
// no keyword for it.
package migrations

import "github.com/pressly/goose/v3"

// Go returns the Go migrations, which the parent package registers with the
// goose provider and counts in LatestVersion.
//
// An explicit slice rather than init() registration: goose's global registry is
// disabled (see newProvider), and a step that announces itself from an init()
// somewhere is a step you cannot find by reading. Adding one means adding a line
// here, next to the file it names.
//
// Versions must not collide with the `.sql` files beside them — one number, one
// step, whichever language it is written in. goose refuses a duplicate at
// provider construction, so a collision fails on the next connect rather than
// silently skipping a step.
func Go() []*goose.Migration {
	return []*goose.Migration{
		retireCuratedNextImportAndOrder(),
	}
}
