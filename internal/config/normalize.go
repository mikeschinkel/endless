package config

import (
	"github.com/mikeschinkel/go-cfgstore"
)

// Normalize applies post-load defaults and validation to an EndlessConfig.
// Called by go-cfgstore after each layer is loaded and before merging.
//
// Intentionally minimal: defaults that depend on layer (like "tracking
// defaults to enforce") are NOT applied here, because applying them in
// the CLI layer would defeat the inherit-from-CLI semantic on the
// project layer. Layer-aware defaults live in the lookup helpers
// instead (see Tracking and Checks accessors).
//
// It does validate every opt-in value (E-2284), here rather than after the
// merge, because only here is the file known: a refusal that cannot say which
// of two files holds the bad value cannot say how to fix it.
func (c *EndlessConfig) Normalize(args cfgstore.NormalizeArgs) error {
	return c.validateOptIns(args.SourceFile)
}
