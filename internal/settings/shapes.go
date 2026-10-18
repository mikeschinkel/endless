package settings

import (
	"path/filepath"
	"strings"
	"unicode"

	"github.com/mikeschinkel/go-doterr"
)

// The shape checks of the path, name and command kinds (E-2401). Exported so
// a feature that reads one of these keys refuses a malformed value with the
// same check the registry applies to its default: the two cannot disagree.
//
// Each checks a value that is present. Whether an empty value means "use the
// default" is the reader's business, as it is for every other kind.

// What each kind takes, as a refusal names it.
const (
	PathAccepts    = "an absolute path, or one under ~"
	NameAccepts    = "a non-empty name with no /, whitespace, : or ."
	CommandAccepts = "a command containing {cmd}, or empty to detect the terminal"
)

// CheckPath accepts an absolute path, or `~` or a path under `~/`, which the
// reader expands to the home directory.
func CheckPath(v string) (err error) {
	if v == "~" || strings.HasPrefix(v, "~/") || filepath.IsAbs(v) {
		goto end
	}
	err = doterr.NewErr(ErrSettings, ErrInvalidValue, ErrNotAbsolutePath, "value", v)
end:
	return err
}

// CheckName accepts a plain file or tmux session name: non-empty, with no `/`
// (it is one path element), no whitespace, and no `:` or `.`, which tmux reads
// as a target's session, window and pane separators.
func CheckName(v string) (err error) {
	if v != "" && !strings.ContainsFunc(v, func(r rune) bool {
		return r == '/' || r == ':' || r == '.' || unicode.IsSpace(r)
	}) {
		goto end
	}
	err = doterr.NewErr(ErrSettings, ErrInvalidValue, ErrMalformedName, "value", v)
end:
	return err
}

// CheckCommand accepts a command template containing `{cmd}`, where the
// command to run goes, or the empty string, which means "detect".
func CheckCommand(v string) (err error) {
	if v == "" || strings.Contains(v, CommandPlaceholder) {
		goto end
	}
	err = doterr.NewErr(ErrSettings, ErrInvalidValue, ErrNoCommandPlaceholder, "value", v)
end:
	return err
}

// CommandPlaceholder is where a command template's command goes.
const CommandPlaceholder = "{cmd}"
