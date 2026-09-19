package model

import "time"

type Voucher struct {
	ID          uint64 `gorm:"primaryKey"`
	ShopID      uint64
	Title       string
	PayValue    uint64
	ActualValue uint64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (Voucher) TableName() string { return "voucher" }

type SeckillVoucher struct {
	VoucherID uint64 `gorm:"primaryKey;autoIncrement:false"`
	Stock     int64
	BeginTime time.Time
	EndTime   time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (SeckillVoucher) TableName() string { return "seckill_voucher" }

// VoucherWithActivity is the result of a single left join. Nil activity columns represent an ordinary voucher.
type VoucherWithActivity struct {
	Voucher
	ActivityID *uint64
	Stock      *int64
	BeginTime  *time.Time
	EndTime    *time.Time
}
