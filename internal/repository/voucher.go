package repository

import (
	"context"
	"errors"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"gorm.io/gorm"
)

type Voucher struct{ db *gorm.DB }

func NewVoucher(db *gorm.DB) *Voucher { return &Voucher{db: db} }
func (r *Voucher) query(ctx context.Context) *gorm.DB {
	// One statement keeps voucher and activity columns in the same MySQL read view.
	return r.db.WithContext(ctx).Model(&model.Voucher{}).
		Select("voucher.*, seckill_voucher.voucher_id AS activity_id, seckill_voucher.stock, seckill_voucher.begin_time, seckill_voucher.end_time").
		Joins("LEFT JOIN seckill_voucher ON seckill_voucher.voucher_id = voucher.id")
}
func (r *Voucher) ByID(ctx context.Context, id uint64) (model.VoucherWithActivity, error) {
	var row model.VoucherWithActivity
	if err := r.query(ctx).Where("voucher.id = ?", id).First(&row).Error; err != nil {
		return model.VoucherWithActivity{}, voucherError(err)
	}
	return row, nil
}
func (r *Voucher) List(ctx context.Context, shopID uint64, offset, limit int) ([]model.VoucherWithActivity, error) {
	query := r.query(ctx)
	if shopID != 0 {
		query = query.Where("voucher.shop_id = ?", shopID)
	}
	var rows []model.VoucherWithActivity
	if err := query.Order("voucher.id ASC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, voucherError(err)
	}
	return rows, nil
}
func voucherError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperror.New(apperror.NotFound, err)
	}
	return apperror.New(apperror.Dependency, err)
}
