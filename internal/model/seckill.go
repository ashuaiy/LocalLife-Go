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
