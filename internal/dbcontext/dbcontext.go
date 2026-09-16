// Package dbcontext resolves WHERE the Endless configuration directory and
// database file live. It is pure path arithmetic: nothing here opens, creates,
// stats or migrates anything, and nothing here consults the working directory.
//
// It exists because two very different programs need the same answer.
// internal/monitor needs it for every application surface and layers its own
// routing on top — the hook's main-database pin, a cwd-self-detected worktree
// sandbox, the E-1429 gate. ED-1571's migration-only executable
// (cmd/endless-migrate) needs the same answer and must link none of that: its
// whole claim to safety is that it carries the migration set and nothing else,
// so importing internal/monitor for one path join would pull the entire
// application into it — including the schema-applying monitor.DB() the
// executable exists in order to avoid.
//
// So the rule has one definition here, and the two callers differ only in what
// they layer on top. internal/schemachange/executable_test.go asserts that the
// executable links nothing but this package and the change applier.
//
// Deliberately absent: any cwd-derived routing. monitor.SelfDetectWorktreeSandbox
// reads the working directory and may redirect a process to a per-worktree
// sandbox. That is right for an application surface and wrong for a migration —
// a tool that rewrites a schema resolves its target from what the caller named,
// never from where it happens to be standing.
package dbcontext

import (
	"os"
	"strings"

	"github.com/mikeschinkel/go-dt"
)

// ConfigDirName is the directory Endless keeps its configuration and database
// in, under the user's configuration root.
const ConfigDirName = "endless"

// DBFileName is the SQLite database's basename inside the configuration
// directory.
const DBFileName = "endless.db"

// ConfigDirFlag is the per-invocation flag naming an explicit configuration —
// and therefore database — directory. A flag rather than an environment
// variable on purpose (E-1429): an exported variable silently routes every
// later command, which is the wrong-database failure the flag exists to stop.
const ConfigDirFlag = "--config-dir"

// ConfigDir returns the Endless configuration directory. An explicit directory
// (ConfigDirFlag) wins; otherwise XDG_CONFIG_HOME, and failing that the home
// directory's .config.
//
// When no home directory can be resolved the result is relative — the shape
// internal/monitor has always produced here, kept because changing it would
// change where every existing surface looks. A caller for which a relative
// database would be a silent disaster rejects one itself: cmd/endless-migrate
// refuses any database path that is not absolute.
func ConfigDir(explicit dt.DirPath) (dir dt.DirPath) {
	var root dt.DirPath
	var home string
	var err error

	if explicit != "" {
		dir = explicit
		goto end
	}

	root = dt.DirPath(os.Getenv("XDG_CONFIG_HOME"))
	if root != "" {
		dir = dt.DirPathJoin(root, ConfigDirName)
		goto end
	}

	home, err = os.UserHomeDir()
	if err != nil {
		// Not an error to report: this function's contract is a path, and
		// every caller has always been handed the join onto whatever
		// os.UserHomeDir returned — the empty string in this case. The
		// resulting relative path fails at the first open, loudly, which is
		// how a caller learns about an unresolvable HOME.
		home = ""
	}
	dir = dt.DirPathJoin3(home, ".config", ConfigDirName)

end:
	return dir
}

// DBPath returns the Endless SQLite database file, for the same explicit
// directory ConfigDir takes.
func DBPath(explicit dt.DirPath) dt.Filepath {
	return dt.FilepathJoin(ConfigDir(explicit), DBFileName)
}

// ConsumeConfigDirFlag strips every "--config-dir <dir>" and "--config-dir=<dir>"
// occurrence out of args, wherever it appears, and returns the remaining args
// alongside the directory the last occurrence named. found distinguishes "no
// such flag" from a flag that named the empty string.
//
// A binary calls this once at the top of main() so its own positional parsing
// (args[1] = subcommand) never sees the flag. A trailing bare "--config-dir"
// with nothing after it names nothing and is dropped.
func ConsumeConfigDirFlag(args []string) (cleaned []string, dir dt.DirPath, found bool) {
	var i int
	var arg string

	cleaned = make([]string, 0, len(args))
	if len(args) == 0 {
		goto end
	}

	cleaned = append(cleaned, args[0])
	for i = 1; i < len(args); i++ {
		arg = args[i]
		switch {
		case arg == ConfigDirFlag:
			if i+1 >= len(args) {
				continue
			}
			dir = dt.DirPath(args[i+1])
			found = true
			i++
		case strings.HasPrefix(arg, ConfigDirFlag+"="):
			dir = dt.DirPath(strings.TrimPrefix(arg, ConfigDirFlag+"="))
			found = true
		default:
			cleaned = append(cleaned, arg)
		}
	}

end:
	return cleaned, dir, found
}
