package config

import (
	"errors"
)

var (
	ErrFailedToLoadConfig    = errors.New("failed to load endless config")
	ErrInvalidProjectPath    = errors.New("invalid project path")
	ErrInvalidTrackingValue  = errors.New("invalid tracking value")
	ErrUnknownConfigDirType  = errors.New("unknown config dir type during merge")
)
