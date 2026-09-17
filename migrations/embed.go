// Package migrations embeds versioned SQL so executables work from any directory.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
