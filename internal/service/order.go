package service

import (
	"context"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"time"
)

// OrderTransaction is the persistence port used only inside a database transaction.
type OrderTransaction interface {
	LockActivity(context.Context, uint64) (model.SeckillVoucher, error)
	Now(context.Context) (time.Time, error)
	HasOrder(context.Context, uint64, uint64) (bool, error)
	DecrementStock(context.Context, uint64) (bool, error)
	CreateOrder(context.Context, model.VoucherOrder) (model.VoucherOrder, error)
}
type Orders interface {
	WithinTransaction(context.Context, func(OrderTransaction) error) error
	ByID(ctx context.Context, userID, id uint64) (model.VoucherOrder, error)
	List(ctx context.Context, userID, voucherID uint64, offset, limit int) ([]model.VoucherOrder, error)
}
type Order struct{ orders Orders }

func NewOrder(orders Orders) *Order { return &Order{orders: orders} }

type OrderView struct {
	ID        uint64    `json:"id,string"`
	UserID    uint64    `json:"user_id,string"`
	VoucherID uint64    `json:"voucher_id,string"`
	Status    uint8     `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}
type OrderPage struct {
	Items    []OrderView `json:"items"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
	HasMore  bool        `json:"has_more"`
}

func (s *Order) Place(ctx context.Context, userID, voucherID uint64) (OrderView, error) {
	if userID == 0 {
		return OrderView{}, apperror.New(apperror.Unauthorized, nil)
	}
	if voucherID == 0 {
		return OrderView{}, apperror.New(apperror.Validation, nil)
	}
	if err := ctx.Err(); err != nil {
		return OrderView{}, err
	}
	var created model.VoucherOrder
	err := s.orders.WithinTransaction(ctx, func(tx OrderTransaction) error {
		activity, err := tx.LockActivity(ctx, voucherID)
		if err != nil {
			return err
		}
		// Read the authoritative clock after acquiring the lock, including any lock wait.
		if activity.AsyncState != 0 {
			return apperror.New(apperror.Conflict, nil)
		}
		now, err := tx.Now(ctx)
		if err != nil {
			return err
		}
		if now.Before(activity.BeginTime) {
			return apperror.New(apperror.ActivityNotStarted, nil)
		}
		if !now.Before(activity.EndTime) {
			return apperror.New(apperror.ActivityEnded, nil)
		}
		exists, err := tx.HasOrder(ctx, userID, voucherID)
		if err != nil {
			return err
		}
		if exists {
			return apperror.New(apperror.AlreadyPurchased, nil)
		}
		changed, err := tx.DecrementStock(ctx, voucherID)
		if err != nil {
			return err
		}
		if !changed {
			return apperror.New(apperror.SoldOut, nil)
		}
		created, err = tx.CreateOrder(ctx, model.VoucherOrder{UserID: userID, VoucherID: voucherID, Status: 1, CreatedAt: now, UpdatedAt: now})
		return err
	})
	if err != nil {
		return OrderView{}, err
	}
	return orderView(created), nil
}
func (s *Order) Detail(ctx context.Context, userID, id uint64) (OrderView, error) {
	if userID == 0 {
		return OrderView{}, apperror.New(apperror.Unauthorized, nil)
	}
	if id == 0 {
		return OrderView{}, apperror.New(apperror.Validation, nil)
	}
	row, err := s.orders.ByID(ctx, userID, id)
	if err != nil {
		return OrderView{}, err
	}
	return orderView(row), nil
}
func (s *Order) List(ctx context.Context, userID, voucherID uint64, page, size int) (OrderPage, error) {
	if userID == 0 {
		return OrderPage{}, apperror.New(apperror.Unauthorized, nil)
	}
	if page < 1 || page > 1000 || size < 1 || size > 50 {
		return OrderPage{}, apperror.New(apperror.Validation, nil)
	}
	rows, err := s.orders.List(ctx, userID, voucherID, (page-1)*size, size+1)
	if err != nil {
		return OrderPage{}, err
	}
	result := OrderPage{Items: make([]OrderView, 0, size), Page: page, PageSize: size, HasMore: len(rows) > size}
	if result.HasMore {
		rows = rows[:size]
	}
	for _, row := range rows {
		result.Items = append(result.Items, orderView(row))
	}
	return result, nil
}
func orderView(row model.VoucherOrder) OrderView {
	return OrderView{ID: row.ID, UserID: row.UserID, VoucherID: row.VoucherID, Status: row.Status, CreatedAt: row.CreatedAt.UTC()}
}
