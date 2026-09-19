package model

import "time"

type ShopType struct {
	ID        uint64 `gorm:"primaryKey"`
	Name      string
	Icon      string
	SortOrder int
}

func (ShopType) TableName() string { return "shop_type" }

type Shop struct {
	ID        uint64 `gorm:"primaryKey"`
	TypeID    uint64
	Name      string
	Address   string
	Longitude float64
	Latitude  float64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (Shop) TableName() string { return "shop" }

// ShopTextEdit changes display fields without changing the GEO/category index.
type ShopTextEdit struct {
	Name    *string
	Address *string
}
