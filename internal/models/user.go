// Package models contains the data structs that mirror the database schema.
package models

import "time"

type User struct {
	ID           string
	Email        string
	PasswordHash string
	Name         string
	IsAdmin      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
