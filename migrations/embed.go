// Package migrations exposes embedded SQL migration files for the database
// migration runner. Files must follow the `NNNN_description.sql` naming
// convention; they are applied in lexicographic order.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
