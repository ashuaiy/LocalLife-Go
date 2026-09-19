package service

import (
	"context"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

type Vouchers interface {
	ByID(context.Context, uint64) (model.VoucherWithActivity, error)
	List(ctx context.Context, shopID uint64, offset, limit int) ([]model.VoucherWithActivity, error)
}
type Voucher struct {
	vouchers Vouchers
	now      func() time.Time
}

func NewVoucher(vouchers Vouchers) *Voucher { return &Voucher{vouchers: vouchers, now: time.Now} }

type VoucherView struct {
	ID          uint64               `json:"id,string"`
	ShopID      uint64               `json:"shop_id,string"`
	Title       string               `json:"title"`
	PayValue    uint64               `json:"pay_value,string"`
	ActualValue uint64               `json:"actual_value,string"`
	Seckill     *VoucherActivityView `json:"seckill"`
}
type VoucherActivityView struct {
	Stock     int64     `json:"stock"`
	BeginTime time.Time `json:"begin_time"`
	EndTime   time.Time `json:"end_time"`
	Status    string    `json:"status"`
}
type VoucherPage struct {
	Items    []VoucherView `json:"items"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	HasMore  bool          `json:"has_more"`
}

func (v *Voucher) Detail(ctx context.Context, id uint64) (VoucherView, error) {
	if id == 0 {
		return VoucherView{}, apperror.New(apperror.Validation, nil)
	}
	if err := ctx.Err(); err != nil {
		return VoucherView{}, err
	}
	row, err := v.vouchers.ByID(ctx, id)
	if err != nil {
		return VoucherView{}, err
	}
	return voucherView(row, v.now())
}
func (v *Voucher) List(ctx context.Context, shopID uint64, page, size int) (VoucherPage, error) {
	if page < 1 || page > 1000 || size < 1 || size > 50 {
		return VoucherPage{}, apperror.New(apperror.Validation, nil)
	}
	if err := ctx.Err(); err != nil {
		return VoucherPage{}, err
	}
	rows, err := v.vouchers.List(ctx, shopID, (page-1)*size, size+1)
	if err != nil {
		return VoucherPage{}, err
	}
	result := VoucherPage{Items: make([]VoucherView, 0, size), Page: page, PageSize: size, HasMore: len(rows) > size}
	if result.HasMore {
		rows = rows[:size]
	}
	now := v.now()
	for _, row := range rows {
		item, err := voucherView(row, now)
		if err != nil {
			return VoucherPage{}, err
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func voucherView(row model.VoucherWithActivity, now time.Time) (VoucherView, error) {
	view := VoucherView{ID: row.ID, ShopID: row.ShopID, Title: row.Title, PayValue: row.PayValue, ActualValue: row.ActualValue}
	if row.ActivityID == nil {
		return view, nil
	}
	if row.Stock == nil || row.BeginTime == nil || row.EndTime == nil {
		return VoucherView{}, apperror.New(apperror.Dependency, nil)
	}
	status := "active"
	switch {
	case now.Before(*row.BeginTime):
		status = "upcoming"
	case !now.Before(*row.EndTime):
		status = "ended"
	case *row.Stock == 0:
		status = "sold_out"
	}
	view.Seckill = &VoucherActivityView{Stock: *row.Stock, BeginTime: row.BeginTime.UTC(), EndTime: row.EndTime.UTC(), Status: status}
	return view, nil
}
