package repository

import (
	"context"
	"errors"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"gorm.io/gorm"
)

type Shop struct{ db *gorm.DB }

func NewShop(db *gorm.DB) *Shop { return &Shop{db: db} }
func (r *Shop) UpdateText(ctx context.Context, id uint64, edit model.ShopTextEdit) (model.Shop, error) {
	changes := map[string]any{}
	if edit.Name != nil {
		changes["name"] = *edit.Name
	}
	if edit.Address != nil {
		changes["address"] = *edit.Address
	}
	var row model.Shop
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Shop{}).Where("id = ?", id).Updates(changes).Error; err != nil {
			return err
		}
		return tx.First(&row, id).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.Shop{}, apperror.New(apperror.NotFound, err)
	}
	if err != nil {
		return model.Shop{}, apperror.New(apperror.Dependency, err)
	}
	return row, nil
}
func (r *Shop) Types(ctx context.Context) ([]model.ShopType, error) {
	var rows []model.ShopType
	if err := r.db.WithContext(ctx).Order("sort_order ASC").Order("id ASC").Find(&rows).Error; err != nil {
		return nil, apperror.New(apperror.Dependency, err)
	}
	return rows, nil
}
func (r *Shop) List(ctx context.Context, typeID uint64, offset, limit int) ([]model.Shop, error) {
	var rows []model.Shop
	query := r.db.WithContext(ctx)
	if typeID != 0 {
		query = query.Where("type_id = ?", typeID)
	}
	if err := query.Order("id ASC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, apperror.New(apperror.Dependency, err)
	}
	return rows, nil
}
func (r *Shop) ByID(ctx context.Context, id uint64) (model.Shop, error) {
	var row model.Shop
	err := r.db.WithContext(ctx).First(&row, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.Shop{}, apperror.New(apperror.NotFound, nil)
	}
	if err != nil {
		return model.Shop{}, apperror.New(apperror.Dependency, err)
	}
	return row, nil
}
