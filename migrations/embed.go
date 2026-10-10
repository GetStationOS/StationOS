// Package migrations supplies immutable, versioned schema migrations.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
