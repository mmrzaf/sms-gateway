// Package migrations embeds the SQL schema migrations so the gateway binary
// can apply them without any files on disk.
package migrations

import "embed"

// FS holds every migration file, named <version>_<name>.sql.
//
//go:embed *.sql
var FS embed.FS
