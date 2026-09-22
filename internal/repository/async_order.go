package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AsyncOrder struct{ db *gorm.DB }

func NewAsyncOrder(db *gorm.DB) *AsyncOrder { return &AsyncOrder{db: db} }

func (r *AsyncOrder) Activity(ctx context.Context, id uint64) (model.SeckillVoucher, error) {
	var row model.SeckillVoucher
	err := r.db.WithContext(ctx).First(&row, "voucher_id = ?", id).Error
	return row, orderError(err)
}

// Stage fences the synchronous writer under the SAME activity lock. Only unused
// activities can change mode. The activation snapshot is never based on later stock.
func (r *AsyncOrder) Stage(ctx context.Context, id uint64) (row model.SeckillVoucher, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "voucher_id = ?", id).Error; err != nil {
			return err
		}
		if row.AsyncState != 0 {
			return nil
		}
		var n int64
		if err := tx.Model(&model.VoucherOrder{}).Where("voucher_id = ?", id).Count(&n).Error; err != nil {
			return err
		}
		var now time.Time
		if err := tx.Raw("SELECT UTC_TIMESTAMP(3)").Row().Scan(&now); err != nil {
			return err
		}
		if n != 0 || row.Stock <= 0 || !now.Before(row.EndTime) {
			return apperror.New(apperror.Conflict, nil)
		}
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return err
		}
		row.AsyncState = 1
		row.AsyncGeneration = hex.EncodeToString(token[:])
		row.AsyncCapacity = row.Stock
		return tx.Model(&model.SeckillVoucher{}).Where("voucher_id = ?", id).Updates(map[string]any{"async_state": 1, "async_generation": row.AsyncGeneration, "async_capacity": row.Stock}).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return row, orderError(err)
}

func (r *AsyncOrder) Activate(ctx context.Context, id uint64, generation string) error {
	return orderError(r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.SeckillVoucher
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "voucher_id = ?", id).Error; err != nil {
			return err
		}
		if row.AsyncGeneration != generation || (row.AsyncState != 1 && row.AsyncState != 2) {
			return apperror.New(apperror.Conflict, nil)
		}
		if row.AsyncState == 2 {
			return nil
		}
		return tx.Model(&row).UpdateColumn("async_state", 2).Error
	}))
}

func (r *AsyncOrder) Result(ctx context.Context, userID, voucherID uint64) (row model.SeckillResult, err error) {
	err = r.db.WithContext(ctx).Where("voucher_id = ? AND user_id = ?", voucherID, userID).Take(&row).Error
	return row, orderError(err)
}

// Apply is safe after a lost commit reply or ACK. The activity lock serializes
// duplicate deliveries, and SQL constraints remain the final backstop.
func (r *AsyncOrder) Apply(ctx context.Context, event model.SeckillEvent) (result model.SeckillResult, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var activity model.SeckillVoucher
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&activity, "voucher_id = ?", event.VoucherID).Error; err != nil {
			return err
		}
		found := tx.Where("voucher_id = ? AND event_id = ?", event.VoucherID, event.ID).Take(&result).Error
		if found == nil {
			if result.UserID != event.UserID || result.Generation != event.Generation || !result.AcceptedAt.Equal(event.AcceptedAt) {
				return apperror.New(apperror.Dependency, errors.New("conflicting seckill event"))
			}
			return nil
		}
		if !errors.Is(found, gorm.ErrRecordNotFound) {
			return found
		}
		if activity.AsyncState != 2 || activity.AsyncGeneration != event.Generation {
			return apperror.New(apperror.Dependency, errors.New("seckill generation is not active"))
		}
		result = model.SeckillResult{VoucherID: event.VoucherID, EventID: event.ID, Generation: event.Generation, UserID: event.UserID, Status: "failed", AcceptedAt: event.AcceptedAt}
		if event.AcceptedAt.Before(activity.BeginTime) || !event.AcceptedAt.Before(activity.EndTime) {
			result.Reason = "outside_activity_window"
		} else {
			var user model.User
			// Hold a shared lock through order insertion so user deletion cannot race the FK.
			userErr := tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("id").First(&user, event.UserID).Error
			if errors.Is(userErr, gorm.ErrRecordNotFound) {
				result.Reason = "user_not_found"
			} else if userErr != nil {
				return userErr
			} else {
				var n int64
				if err := tx.Model(&model.VoucherOrder{}).Where("voucher_id = ? AND user_id = ?", event.VoucherID, event.UserID).Count(&n).Error; err != nil {
					return err
				}
				if n != 0 {
					result.Reason = "already_purchased"
				} else if activity.Stock == 0 {
					result.Reason = "sold_out"
				} else {
					changed := tx.Model(&model.SeckillVoucher{}).Where("voucher_id = ? AND stock > 0", event.VoucherID).UpdateColumn("stock", gorm.Expr("stock - 1"))
					if changed.Error != nil {
						return changed.Error
					}
					if changed.RowsAffected != 1 {
						return errors.New("seckill stock changed under lock")
					}
					order := model.VoucherOrder{UserID: event.UserID, VoucherID: event.VoucherID, Status: 1}
					if err := tx.Create(&order).Error; err != nil {
						return err
					}
					result.Status = "created"
					result.OrderID = &order.ID
				}
			}
		}
		return tx.Create(&result).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	// SQL failures are retryable; never interpret a failed transaction as a final failure.
	if err != nil {
		return model.SeckillResult{}, apperror.New(apperror.Dependency, err)
	}
	return result, nil
}

func (r *AsyncOrder) ActiveIDs(ctx context.Context, after uint64, limit int) ([]uint64, error) {
	var ids []uint64
	err := r.db.WithContext(ctx).Model(&model.SeckillVoucher{}).Where("async_state = 2 AND voucher_id > ?", after).Order("voucher_id").Limit(limit).Pluck("voucher_id", &ids).Error
	return ids, orderError(err)
}
