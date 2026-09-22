package model

import "time"

// SeckillSeed is an immutable activation snapshot supplied by the controlled
// activation flow, never reconstructed automatically by an HTTP request.
type SeckillSeed struct {
	VoucherID  uint64
	Generation string
	Stock      int64
	BeginTime  time.Time
	EndTime    time.Time
}

// SeckillEvent is an accepted reservation, not a committed MySQL order.
type SeckillEvent struct {
	ID         string
	VoucherID  uint64
	UserID     uint64
	Generation string
	AcceptedAt time.Time
}

// SeckillResult is committed in the same transaction as the order and stock.
type SeckillResult struct {
	VoucherID  uint64 `gorm:"primaryKey;autoIncrement:false"`
	EventID    string `gorm:"primaryKey"`
	Generation string
	UserID     uint64
	OrderID    *uint64
	Status     string
	Reason     string
	AcceptedAt time.Time
	CreatedAt  time.Time
}

func (SeckillResult) TableName() string { return "seckill_result" }
