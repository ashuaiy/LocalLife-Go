package model

import "time"

type Blog struct {
	ID        uint64 `gorm:"primaryKey"`
	UserID    uint64
	ShopID    uint64
	Title     string
	Content   string
	Images    string
	Nickname  string `gorm:"->"`
	Avatar    string `gorm:"->"`
	CreatedAt time.Time
	UpdatedAt time.Time
	LikeCount int64 `gorm:"->"`
}

func (Blog) TableName() string { return "blog" }

type BlogLike struct {
	BlogID    uint64 `gorm:"primaryKey;autoIncrement:false"`
	UserID    uint64 `gorm:"primaryKey;autoIncrement:false"`
	CreatedAt time.Time
}

func (BlogLike) TableName() string { return "blog_like" }
