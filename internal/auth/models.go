package auth

import "time"

type User struct {
	ID           int64
	Email        string
	Name         string
	PasswordHash string
	IsAdmin      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Session struct {
	ID        string
	UserID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
	UserAgent string
	IPAddress string
}
