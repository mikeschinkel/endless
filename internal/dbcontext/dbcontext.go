// Package dbcontext owns Endless's database-target vocabulary: the flags that
// name a database, the words those flags take, and the path each word resolves
// to. It is pure path arithmetic and pure argv arithmetic — nothing here opens,
// creates, stats or migrates anything.
//
// It exists because two very different programs need the same answer.
// internal/monitor needs it for every application surface and layers its own
// routing on top — the hook's main-database pin, the E-1429 gate, and the one
// word this package deliberately cannot resolve. ED-1571's migration-only
// executable (cmd/endless-migrate) needs the same answer and must link none of
// that: its whole claim to safety is that it carries the migration set and
// nothing else, so importing internal/monitor for one path join would pull the
// entire application into it — including the schema-applying monitor.DB() the
// executable exists in order to avoid.
//
// So the rule has one definition here, and the two callers differ only in what
// they layer on top. internal/schemachange/executable_test.go asserts that the
// executable links nothing but this package and the change applier.
//
// # The vocabulary
//
//	--db main        the deployed installation's database ($HOME-following)
//	--db sandbox     this worktree's sandbox database, addressed from cwd
//	--db-dir <path>  name a directory outright
//
// ConsumeFlags parses all three and resolves two. `--db sandbox` it returns as
// a CHOICE rather than a path, because resolving it means reading the working
// directory and this package never does (see below). monitor resolves it;
// cmd/endless-migrate refuses it, and can only refuse it because the parse
// hands it a word to refuse rather than an unrecognised argument to trip over.
//
// Knowing the word and resolving the word are different jobs. This package owns
// the first for all three — which is what makes "one vocabulary" true rather
// than aspirational — and the second for the two that need no cwd.
//
// # Deliberately absent: any cwd-derived routing
//
// monitor resolves `--db sandbox` by reading the working directory to find
// WHICH worktree's sandbox is meant. That is right for an application surface
// and wrong for a migration — a tool that rewrites a schema resolves its target
// from what the caller named, never from where it happens to be standing.
//
// E-1368 once let cwd route a process on its own, with no flag at all; E-1668
// removed that, because the same routing satisfied the E-1429 gate and so let a
// database be opened that nobody had chosen. Detection may decide where to look;
// only a flag decides that you may open it.
//
// # Main is not Default
//
// ConfigDir resolves the DEFAULT target: XDG_CONFIG_HOME, then $HOME/.config.
// MainConfigDir resolves `--db main`, which skips XDG_CONFIG_HOME entirely.
//
// The two are not the same function waiting to be merged. Endless injects
// XDG_CONFIG_HOME to route a child process at a worktree's sandbox
// (sandboxcmd.Sandbox.Env, minimizerjob), so escaping that injection
// is the whole meaning of asking for main. E-1964 deleted `sandbox bind` — the
// PERSISTENT injection written into a settings file — but the per-invocation
// one remains, by design, and so does this distinction.
//
// Following $HOME rather than hardcoding a path is what lets a verify suite,
// which runs under a temp HOME, say `--db main` and mean its own isolated main
// rather than the developer's real one.
package dbcontext

import (
	"errors"
	"fmt"
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

// configRootName is the conventional per-user configuration root under $HOME.
// Named once so "the main database" is spelled in exactly one place; six copies
// of this join across internal/monitor and internal/sandboxcmd are what E-2157
// collapsed into this package.
const configRootName = ".config"

// DBFlag names a database by NAME — the vocabulary a reader learns from the
// guide and types at the Python CLI, so that the word a user says is the word
// the binary hears (E-1668).
const DBFlag = "--db"

// DBDirFlag names a configuration directory outright. It is the escape for a
// caller that must name a directory WITHOUT owning the process environment —
// chiefly Go tests on t.TempDir(). A caller that already runs under a temp HOME
// (the verify runner does) wants plain `--db main`, since MainConfigDir follows
// $HOME.
const DBDirFlag = "--db-dir"

// ConfigDirFlag is the retired spelling, recognised only so it can be refused
// by name. E-1668 replaced it on endless-go and E-2157 on cmd/endless-migrate;
// recognising it costs one switch arm and turns a reader's muscle memory into a
// one-word fix instead of "unknown command --config-dir", which names no
// remedy at all.
const ConfigDirFlag = "--config-dir"

// Choice is WHICH database an invocation named. It is the parsed word, not a
// resolved path: ChoiceSandbox in particular has no path in this package, since
// resolving it requires the working directory.
type Choice int

const (
	// ChoiceNone means no database flag appeared. The caller falls back to
	// ConfigDir's default resolution, or refuses — monitor's E-1429 gate
	// refuses inside a self-dev worktree, where both databases are reachable
	// and silence chooses neither.
	ChoiceNone Choice = iota

	// ChoiceMain is `--db main`, resolved by MainConfigDir.
	ChoiceMain

	// ChoiceSandbox is `--db sandbox`. Returned unresolved: see the package
	// doc. monitor resolves it from cwd; cmd/endless-migrate refuses it.
	ChoiceSandbox

	// ChoiceDir is `--db-dir <path>`, whose path is Flags.Dir.
	ChoiceDir
)

// String renders a Choice as the word a caller typed, so a refusal can quote
// the invocation back rather than an internal number.
func (c Choice) String() (s string) {
	switch c {
	case ChoiceMain:
		s = "main"
	case ChoiceSandbox:
		s = "sandbox"
	case ChoiceDir:
		s = DBDirFlag
	default:
		s = "none"
	}
	return s
}

// Flags is one invocation's database target, as named on the command line.
type Flags struct {
	Choice Choice
	Dir    dt.DirPath // set only when Choice is ChoiceDir
}

// ErrDBFlagConflict is returned when one invocation spells its choice twice.
var ErrDBFlagConflict = errors.New(
	DBFlag + " and " + DBDirFlag + " are two spellings of one choice; pass only one")

// ErrDBFlagNeedsValue is returned for a trailing bare --db. A missing value is
// an error rather than a silent skip: it is a caller that meant to choose and
// did not, and tolerating it silently is how a choice goes missing.
var ErrDBFlagNeedsValue = errors.New(DBFlag + " requires a value: main or sandbox")

// ErrDBDirFlagNeedsDir is ErrDBFlagNeedsValue's sibling for --db-dir.
var ErrDBDirFlagNeedsDir = errors.New(DBDirFlag + " requires a directory")

// ErrConfigDirFlagRetired names the replacement rather than merely rejecting.
var ErrConfigDirFlagRetired = errors.New(
	ConfigDirFlag + " was renamed to " + DBDirFlag +
		"; pass " + DBFlag + " main for the main database")

// ErrUnknownDBValue is the parse failure for a --db value outside the
// vocabulary. Wrapped with the offending word, so errors.Is identifies the
// class and the message names the instance.
var ErrUnknownDBValue = errors.New("unknown " + DBFlag + " value")

// ConsumeFlags strips the database-target flags out of args, wherever they
// appear, and returns the remaining args alongside the target they named.
//
// A binary calls this once at the top of main() so its own positional parsing
// (args[1] = subcommand) never sees the flags, which is what makes
// `endless-go --db main event emit ...` work without touching the subcommand
// parsers. Pure by design: it takes args and returns args, so the caller owns
// the decision to write os.Args back.
//
// Both flags accept the `--flag value` and `--flag=value` forms.
//
// The target is carried as a per-invocation flag, never an env var: an exported
// env var could silently satisfy the E-1429 gate for every later command, which
// is exactly the silent-wrong-DB failure mode that gate exists to prevent — and
// since E-1668, never from cwd either.
func ConsumeFlags(args []string) (cleaned []string, flags Flags, err error) {
	var i int
	var arg string
	var word string
	var dir dt.DirPath
	var haveWord, haveDir bool

	cleaned = make([]string, 0, len(args))
	if len(args) == 0 {
		goto end
	}

	cleaned = append(cleaned, args[0])
	for i = 1; i < len(args); i++ {
		arg = args[i]
		switch {
		case arg == DBFlag:
			if i+1 >= len(args) {
				err = ErrDBFlagNeedsValue
				goto end
			}
			word, haveWord = args[i+1], true
			i++
		case strings.HasPrefix(arg, DBFlag+"="):
			word, haveWord = strings.TrimPrefix(arg, DBFlag+"="), true
		case arg == DBDirFlag:
			if i+1 >= len(args) {
				err = ErrDBDirFlagNeedsDir
				goto end
			}
			dir, haveDir = dt.DirPath(args[i+1]), true
			i++
		case strings.HasPrefix(arg, DBDirFlag+"="):
			dir, haveDir = dt.DirPath(strings.TrimPrefix(arg, DBDirFlag+"=")), true
		case arg == ConfigDirFlag, strings.HasPrefix(arg, ConfigDirFlag+"="):
			err = ErrConfigDirFlagRetired
			goto end
		default:
			cleaned = append(cleaned, arg)
		}
	}

	if haveWord && haveDir {
		err = ErrDBFlagConflict
		goto end
	}

	switch {
	case haveDir:
		flags = Flags{Choice: ChoiceDir, Dir: dir}
	case !haveWord:
		// ChoiceNone — the zero value, and the caller's business.
	case word == ChoiceMain.String():
		flags = Flags{Choice: ChoiceMain}
	case word == ChoiceSandbox.String():
		flags = Flags{Choice: ChoiceSandbox}
	default:
		err = fmt.Errorf("%w %q: expected %q or %q",
			ErrUnknownDBValue, word, ChoiceMain.String(), ChoiceSandbox.String())
	}

end:
	return cleaned, flags, err
}

// ConfigDir returns the DEFAULT Endless configuration directory. An explicit
// directory (ChoiceDir) wins; otherwise XDG_CONFIG_HOME, and failing that the
// home directory's .config.
//
// This is not MainConfigDir and does not become it — see "Main is not Default"
// in the package doc.
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
	dir = dt.DirPathJoin3(home, configRootName, ConfigDirName)

end:
	return dir
}

// DBPath returns the Endless SQLite database file, for the same explicit
// directory ConfigDir takes.
func DBPath(explicit dt.DirPath) dt.Filepath {
	return dt.FilepathJoin(ConfigDir(explicit), DBFileName)
}

// MainConfigDir resolves `--db main`: the deployed installation's configuration
// directory. It FOLLOWS $HOME while deliberately ignoring $XDG_CONFIG_HOME,
// which is the whole point of asking for main — to escape a sandbox the
// environment routed this process into.
//
// Mirrors Python's config.main_config_dir, so "the main database" means one
// thing across both layers. Unlike ConfigDir it reports an unresolvable HOME
// rather than returning a relative path: a caller that NAMED main has been
// specific, and handing it a silently relative answer would defeat that.
func MainConfigDir() (dir dt.DirPath, err error) {
	var home string

	home, err = os.UserHomeDir()
	if err != nil {
		err = fmt.Errorf("resolving home directory for %s main: %w", DBFlag, err)
		goto end
	}
	dir = dt.DirPathJoin3(home, configRootName, ConfigDirName)

end:
	return dir, err
}

// MainDBPath is MainConfigDir's database file — the spelling the three
// pin-the-real-database callers in internal/monitor want.
func MainDBPath() (path dt.Filepath, err error) {
	var dir dt.DirPath

	dir, err = MainConfigDir()
	if err != nil {
		goto end
	}
	path = dt.FilepathJoin(dir, DBFileName)

end:
	return path, err
}
