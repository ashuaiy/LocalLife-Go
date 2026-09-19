package model

import "time"

type VoucherOrder struct {
	ID        uint64 `gorm:"primaryKey"`
	UserID    uint64
	VoucherID uint64
	Status    uint8
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (VoucherOrder) TableName() string { return "voucher_order" }
