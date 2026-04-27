// Package schema embeds the SQL schema for the Endless database.
package schema

import (
	_ "embed"
)

//go:embed schema.sql
var SQL string
