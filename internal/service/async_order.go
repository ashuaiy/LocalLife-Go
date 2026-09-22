package service

import (
	"context"
	"errors"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"time"
)

type AsyncOrders interface {
	Activity(context.Context, uint64) (model.SeckillVoucher, error)
	Stage(context.Context, uint64) (model.SeckillVoucher, error)
	Activate(context.Context, uint64, string) error
	Result(context.Context, uint64, uint64) (model.SeckillResult, error)
	Apply(context.Context, model.SeckillEvent) (model.SeckillResult, error)
	ActiveIDs(context.Context, uint64, int) ([]uint64, error)
}
type SeckillQueue interface {
	Prepare(context.Context, model.SeckillSeed) error
	Check(context.Context, model.SeckillSeed, bool) error
	ReserveForGeneration(context.Context, uint64, uint64, string) (model.SeckillEvent, error)
	Receipt(context.Context, uint64, uint64) (model.SeckillEvent, error)
	Read(context.Context, uint64, string, int64) ([]model.SeckillEvent, error)
	Claim(context.Context, uint64, string, string, time.Duration, int64) ([]model.SeckillEvent, string, error)
	Compensate(context.Context, model.SeckillEvent) error
	Ack(context.Context, model.SeckillEvent) error
}
type AsyncOrder struct {
	orders AsyncOrders
	queue  SeckillQueue
}

func NewAsyncOrder(orders AsyncOrders, queue SeckillQueue) *AsyncOrder {
	return &AsyncOrder{orders: orders, queue: queue}
}

type SeckillView struct {
	VoucherID uint64  `json:"voucher_id,string"`
	Ticket    string  `json:"ticket"`
	Status    string  `json:"status"`
	OrderID   *uint64 `json:"order_id,string,omitempty"`
	Reason    string  `json:"reason,omitempty"`
}

func (s *AsyncOrder) Enable(ctx context.Context, id uint64) error {
	if id == 0 {
		return apperror.New(apperror.Validation, nil)
	}
	row, err := s.orders.Stage(ctx, id)
	if err != nil {
		return err
	}
	seed := model.SeckillSeed{VoucherID: id, Generation: row.AsyncGeneration, Stock: row.AsyncCapacity, BeginTime: row.BeginTime, EndTime: row.EndTime}
	if row.AsyncState == 2 {
		return s.queue.Check(ctx, seed, false)
	}
	if err := s.queue.Prepare(ctx, seed); err != nil {
		var classified *apperror.Error
		// Existing Redis keys may be the result of a crash before SQL activation.
		_, code, _ := apperror.Describe(err)
		if !errors.As(err, &classified) || code != "conflict" {
			return err
		}
	}
	if err := s.queue.Check(ctx, seed, true); err != nil {
		return err
	}
	return s.orders.Activate(ctx, id, row.AsyncGeneration)
}
func (s *AsyncOrder) Accept(ctx context.Context, userID, voucherID uint64) (SeckillView, error) {
	if userID == 0 {
		return SeckillView{}, apperror.New(apperror.Unauthorized, nil)
	}
	if voucherID == 0 {
		return SeckillView{}, apperror.New(apperror.Validation, nil)
	}
	row, err := s.orders.Activity(ctx, voucherID)
	if err != nil {
		return SeckillView{}, err
	}
	if row.AsyncState != 2 {
		return SeckillView{}, apperror.New(apperror.Conflict, nil)
	}
	event, err := s.queue.ReserveForGeneration(ctx, voucherID, userID, row.AsyncGeneration)
	if err != nil {
		return SeckillView{}, err
	}
	return SeckillView{VoucherID: voucherID, Ticket: event.ID, Status: "pending"}, nil
}
func (s *AsyncOrder) Result(ctx context.Context, userID, voucherID uint64) (SeckillView, error) {
	if userID == 0 {
		return SeckillView{}, apperror.New(apperror.Unauthorized, nil)
	}
	if voucherID == 0 {
		return SeckillView{}, apperror.New(apperror.Validation, nil)
	}
	result, err := s.orders.Result(ctx, userID, voucherID)
	if err == nil {
		return SeckillView{VoucherID: voucherID, Ticket: result.EventID, Status: result.Status, OrderID: result.OrderID, Reason: result.Reason}, nil
	}
	_, code, _ := apperror.Describe(err)
	if code != "not_found" {
		return SeckillView{}, err
	}
	row, err := s.orders.Activity(ctx, voucherID)
	if err != nil {
		return SeckillView{}, err
	}
	if row.AsyncState != 2 {
		return SeckillView{}, apperror.New(apperror.Conflict, nil)
	}
	event, err := s.queue.Receipt(ctx, voucherID, userID)
	if err != nil {
		return SeckillView{}, err
	}
	if event.Generation != row.AsyncGeneration {
		return SeckillView{}, apperror.New(apperror.Dependency, nil)
	}
	return SeckillView{VoucherID: voucherID, Ticket: event.ID, Status: "pending"}, nil
}
func (s *AsyncOrder) Process(ctx context.Context, event model.SeckillEvent) error {
	result, err := s.orders.Apply(ctx, event)
	if err != nil {
		return err
	}
	if result.Status == "failed" {
		if err := s.queue.Compensate(ctx, event); err != nil {
			return err
		}
	}
	return s.queue.Ack(ctx, event)
}
