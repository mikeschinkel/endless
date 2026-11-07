package gomodcheck

import (
	"errors"
)

var (
	ErrFindingModules = errors.New("searching the worktree for go.mod files")
	ErrResolvingMain  = errors.New("resolving the main checkout root")
	ErrReadingModfile = errors.New("reading go.mod")
	ErrWritingModfile = errors.New("writing the re-rooted go.mod copy")
	ErrCopyingSum     = errors.New("copying go.sum beside the re-rooted go.mod")
	ErrMakingTempDir  = errors.New("creating the temporary directory for the re-rooted go.mod")
	ErrGoVersion      = errors.New("asking go for its version")
	ErrGoTooOld       = errors.New("go mod tidy -diff needs Go 1.23 or later")
	ErrRunningTidy    = errors.New("running go mod tidy -diff")
)
