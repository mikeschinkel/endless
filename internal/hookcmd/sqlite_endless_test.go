package hookcmd

import "testing"

func TestSqliteEndlessRe(t *testing.T) {
	cases := []struct {
		name    string
		command string
		match   bool
	}{
		// Should block
		{"bare relative", "sqlite3 .endless/db.sqlite", true},
		{"with ./ prefix", "sqlite3 ./.endless/anything", true},
		{"deep path", "sqlite3 path/to/.endless/x", true},
		{"absolute path", "sqlite3 /Users/foo/projects/bar/.endless/y", true},
		{"with leading env", "PATH=/foo sqlite3 .endless/db", true},
		{"uppercase", "SQLITE3 .endless/db", true},
		{"with .sql command", "sqlite3 .endless/db.sqlite \".tables\"", true},

		// Should NOT block
		{"different db", "sqlite3 /tmp/x.db", false},
		{"real endless DB", "sqlite3 /Users/foo/.config/endless/endless.db", false},
		{"no sqlite3", "ls .endless/", false},
		{"endless suffix not prefix", "sqlite3 my.endless/db", false},
		{"piped after", "echo .endless/ | sqlite3 /tmp/x.db", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sqliteEndlessRe.MatchString(tc.command)
			if got != tc.match {
				t.Errorf("MatchString(%q) = %v, want %v", tc.command, got, tc.match)
			}
		})
	}
}
