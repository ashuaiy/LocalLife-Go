package model

import "time"

type Follow struct {
	ID           uint64 `gorm:"primaryKey"`
	UserID       uint64
	FollowUserID uint64
	CreatedAt    time.Time
}

func (Follow) TableName() string { return "follow" }

type FeedEntry struct {
	BlogID uint64
	Score  int64
}
