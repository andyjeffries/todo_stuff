package models

import "time"

type Session struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

func (s *Session) IsExpired(now time.Time) bool {
	return !s.ExpiresAt.After(now)
}
