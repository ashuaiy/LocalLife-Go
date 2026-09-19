package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

type Order struct{ db *gorm.DB }

func NewOrder(db *gorm.DB) *Order { return &Order{db: db} }
func (r *Order) WithinTransaction(ctx context.Context, fn func(service.OrderTransaction) error) error {
	return orderError(r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&orderTransaction{db: tx})
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted}))
}
func (r *Order) ByID(ctx context.Context, userID, id uint64) (model.VoucherOrder, error) {
	var row model.VoucherOrder
	if err := r.db.WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).First(&row).Error; err != nil {
		return model.VoucherOrder{}, orderError(err)
	}
	return row, nil
}
func (r *Order) List(ctx context.Context, userID, voucherID uint64, offset, limit int) ([]model.VoucherOrder, error) {
	query := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if voucherID != 0 {
		query = query.Where("voucher_id = ?", voucherID)
	}
	var rows []model.VoucherOrder
	if err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, orderError(err)
	}
	return rows, nil
}

type orderTransaction struct{ db *gorm.DB }

func (t *orderTransaction) LockActivity(ctx context.Context, id uint64) (model.SeckillVoucher, error) {
	var row model.SeckillVoucher
	err := t.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "voucher_id = ?", id).Error
	return row, orderError(err)
}
func (t *orderTransaction) Now(ctx context.Context) (time.Time, error) {
	var now time.Time
	err := t.db.WithContext(ctx).Raw("SELECT UTC_TIMESTAMP(3)").Row().Scan(&now)
	return now, orderError(err)
}
func (t *orderTransaction) HasOrder(ctx context.Context, userID, voucherID uint64) (bool, error) {
	var row model.VoucherOrder
	err := t.db.WithContext(ctx).Select("id").Where("user_id = ? AND voucher_id = ?", userID, voucherID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, orderError(err)
}
func (t *orderTransaction) DecrementStock(ctx context.Context, id uint64) (bool, error) {
	result := t.db.WithContext(ctx).Model(&model.SeckillVoucher{}).Where("voucher_id = ? AND stock > 0", id).UpdateColumn("stock", gorm.Expr("stock - 1"))
	return result.RowsAffected == 1, orderError(result.Error)
}
func (t *orderTransaction) CreateOrder(ctx context.Context, row model.VoucherOrder) (model.VoucherOrder, error) {
	err := t.db.WithContext(ctx).Create(&row).Error
	return row, orderError(err)
}
func orderError(err error) error {
	if err == nil {
		return nil
	}
	var classified *apperror.Error
	if errors.As(err, &classified) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, gorm.ErrForeignKeyViolated) {
		return apperror.New(apperror.NotFound, err)
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return apperror.New(apperror.AlreadyPurchased, err)
	}
	return apperror.New(apperror.Dependency, err)
}
