// Package models contains the data structs that mirror the database schema.
package models

import (
	"database/sql"
	"time"
)

type User struct {
	ID              string
	Email           string
	PasswordHash    string
	Name            string
	IsAdmin         bool
	PushoverUserKey sql.NullString
	PushoverEnabled bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
