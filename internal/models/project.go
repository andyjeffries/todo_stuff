package models

import (
	"database/sql"
	"time"
)

// Project mirrors the projects row. Icon and Color are nullable in the schema;
// the rest are required.
type Project struct {
	ID        string
	UserID    string
	Name      string
	Icon      sql.NullString
	Color     sql.NullString
	Position  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}
