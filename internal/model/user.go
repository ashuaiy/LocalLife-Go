package model

import "time"

// User is a persistence entity. Handlers return an explicit public projection.
type User struct {
	ID        uint64 `gorm:"primaryKey"`
	Phone     string
	Nickname  string
	Avatar    string
	CreatedAt time.Time
	UpdatedAt time.Time
}
