package refusal

import (
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
)

// Diagnostics are not user-facing output, and this file is where that
// distinction is enforced.
//
// Both loggers used to write to stderr. The standard logger was pointed there
// by hookcmd's init(), which ran for EVERY endless-go subcommand — `task-status
// get`, `markdown render`, anything — before --db had even been parsed, and
// redirected the standard logger whether or not a hook was involved. The slog
// handler behind go-cfgstore wrote there too. Neither is a message anybody
// asked for, and both landed in the same stream as classified refusals, where
// an agent reads them as output of the command it just ran.
//
// So they go to Endless's own log file and nowhere else, and this package owns
// the decision because this package owns stderr. A log line the user needs to
// see is not a log line — it is a Warn, or a faults.Record.
//
// Failure to open the log file degrades to io.Discard rather than falling back
// to stderr. Falling back is what put diagnostics in front of an agent in the
// first place, and a missing log file is not worth corrupting a hook's block
// reason over.

// LogDirName is the subdirectory of the Endless config directory that holds
// these files. It matches the directory `endless errors show --detail` reads
// its JSONL from, so everything diagnostic lives in one place.
const LogDirName = "log"

// InitLog points the standard logger at file within the config directory's log
// subdirectory, and gives it prefix.
//
// Callers pass configDir rather than this package resolving it: internal/monitor
// owns config-directory resolution and imports this package, so the dependency
// can only run one way.
//
// Call it from the subcommand that logs, not from an init(): an init() runs for
// every subcommand of the binary it is linked into, which is how this setup
// came to redirect logging for commands that never log at all.
func InitLog(configDir, file, prefix string) {
	log.SetOutput(logWriter(configDir, file))
	log.SetFlags(log.Ldate | log.Ltime)
	log.SetPrefix(prefix)
}

// SlogHandler is a slog handler over the same log file, for libraries that take
// one — go-cfgstore requires a package-global logger and panics without it.
func SlogHandler(configDir, file string, level slog.Level) slog.Handler {
	return slog.NewTextHandler(logWriter(configDir, file), &slog.HandlerOptions{
		Level: level,
	})
}

// logWriter returns a writer that opens configDir/log/file for append on its
// first write, or discards if it cannot.
//
// Lazily, because SlogHandler is installed by every endless-go subcommand and
// most of them never log. Opening at construction left an empty log/ in every
// config dir a run touched, and the verify runner's config dir must hold only
// the config it wrote: the guard reads anything more as a real config and
// refuses the next harness check (E-2296).
func logWriter(configDir, file string) io.Writer {
	return &lazyLogFile{path: filepath.Join(configDir, LogDirName, file)}
}

// lazyLogFile is logWriter's writer. Not safe for concurrent first writes from
// separate goroutines; slog's handlers and the standard logger both serialize
// their writes, which is all that uses it.
type lazyLogFile struct {
	path string
	w    io.Writer
}

func (l *lazyLogFile) Write(p []byte) (int, error) {
	if l.w == nil {
		l.w = openLogFile(l.path)
	}
	return l.w.Write(p)
}

// openLogFile opens path for append, creating its directory, or returns
// io.Discard.
func openLogFile(path string) io.Writer {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return io.Discard
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return io.Discard
	}
	return f
}
