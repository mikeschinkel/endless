package schemachange

import (
	"errors"
)

var (
	ErrApplyingChange    = errors.New("applying schema change")
	ErrChangeNotFound    = errors.New("schema-change file not found")
	ErrUnsupportedChange = errors.New("unsupported schema-change extension (only .sql and .go)")
	ErrReadingChange     = errors.New("reading schema-change file")

	ErrEnsuringVersionTable = errors.New("ensuring the _schema_version table")
	ErrReadingMarker        = errors.New("reading the _schema_version marker")
	ErrRecordingMarker      = errors.New("recording the _schema_version marker")

	ErrBeginning   = errors.New("beginning the transaction")
	ErrCommitting  = errors.New("committing the transaction")
	ErrRollingBack = errors.New("rolling back the transaction")

	ErrRunningChangeScript = errors.New("running the .go change script")
	ErrResolvingChangePath = errors.New("resolving the schema-change path")
)
